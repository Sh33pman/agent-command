package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// What Claude sends when background work finishes, as its own prompt.
func wakeupPrompt(id string) string {
	return `<task-notification>\n<task-id>` + id + `</task-id>\n<status>completed</status>\n</task-notification>`
}

func TestBackgroundWorkIsNotifiedWhenItFinishes(t *testing.T) {
	d, clock, _ := notifyDaemon(t, config.Default().Notify)
	ctx := context.Background()
	t0 := *clock
	hook := func(after time.Duration, name, payload string) {
		t.Helper()
		*clock = t0.Add(after)
		must(t, d.ApplyHook(ctx, hookEvent(name, payload, *clock, ipc.HookContext{})))
	}
	row := func() *core.Session {
		t.Helper()
		snap, err := d.snapshot(ctx)
		must(t, err)
		if len(snap.Sessions) != 1 {
			t.Fatalf("sessions: %d", len(snap.Sessions))
		}
		return snap.Sessions[0]
	}
	kinds := func() (out []string) {
		for _, n := range d.notices() {
			out = append(out, n.Kind)
		}
		return out
	}

	hook(0, "SessionStart", `{"session_id":"c1","source":"startup","cwd":"/w/api"}`)
	hook(time.Second, "UserPromptSubmit", `{"session_id":"c1","prompt":"run the build"}`)
	hook(3*time.Second, "PostToolUse", `{"session_id":"c1","tool_name":"Bash","tool_response":{"backgroundTaskId":"b1","stdout":""}}`)
	hook(4*time.Second, "PostToolUse", `{"session_id":"c1","tool_name":"Bash","tool_response":{"stdout":"ok"}}`)
	hook(5*time.Second, "Stop", `{"session_id":"c1"}`)
	if k := kinds(); len(k) != 0 {
		t.Errorf("a 4-second turn notified: %v", k)
	}
	if s := row(); s.Status != core.StatusDone || s.StatusDetail != BackgroundDetail || s.Name != "run the build" {
		t.Errorf("while it runs: %s %q %q", s.Status, s.StatusDetail, s.Name)
	}

	// Ten minutes later the build finishes, and Claude wakes up for a second.
	hook(10*time.Minute, "UserPromptSubmit", `{"session_id":"c1","prompt":"`+wakeupPrompt("b1")+`"}`)
	if s := row(); s.Status != core.StatusBusy || s.StatusDetail != "" || s.Name != "run the build" {
		t.Errorf("woken: %s %q %q", s.Status, s.StatusDetail, s.Name)
	}
	hook(10*time.Minute+time.Second, "Stop", `{"session_id":"c1"}`)
	if k := kinds(); len(k) != 1 || k[0] != "done" {
		t.Fatalf("finished work: %v", k)
	}
	if s := row(); s.StatusDetail != "" {
		t.Errorf("after: %q", s.StatusDetail)
	}

	// The next short turn is short again.
	hook(11*time.Minute, "UserPromptSubmit", `{"session_id":"c1","prompt":"thanks"}`)
	hook(11*time.Minute+2*time.Second, "Stop", `{"session_id":"c1"}`)
	if k := kinds(); len(k) != 1 {
		t.Errorf("a short turn after it: %v", k)
	}
}

func TestBackgroundAgentsAndWorkThatEnds(t *testing.T) {
	d, clock, _ := notifyDaemon(t, config.Default().Notify)
	ctx := context.Background()
	t0 := *clock
	hook := func(after time.Duration, name, payload string) {
		t.Helper()
		*clock = t0.Add(after)
		must(t, d.ApplyHook(ctx, hookEvent(name, payload, *clock, ipc.HookContext{})))
	}
	hook(0, "SessionStart", `{"session_id":"c1","source":"startup"}`)
	hook(time.Second, "UserPromptSubmit", `{"session_id":"c1","prompt":"review it"}`)
	hook(2*time.Second, "PostToolUse", `{"session_id":"c1","tool_name":"Agent","tool_response":{"status":"async_launched","agentId":"a7"}}`)
	hook(3*time.Second, "PostToolUse", `{"session_id":"c1","tool_name":"Bash","tool_response":{"backgroundTaskId":"b2"}}`)
	hook(4*time.Second, "Stop", `{"session_id":"c1"}`)
	if got := len(d.work().running[sessionID(t, d)]); got != 2 {
		t.Fatalf("following %d", got)
	}

	// A wake-up for work it never saw start changes nothing.
	hook(time.Minute, "UserPromptSubmit", `{"session_id":"c1","prompt":"`+wakeupPrompt("zz")+`"}`)
	hook(time.Minute+time.Second, "Stop", `{"session_id":"c1"}`)
	if len(d.notices()) != 0 {
		t.Errorf("unknown work notified: %+v", d.notices())
	}

	// The agent finishes: notified, while the command still runs.
	hook(5*time.Minute, "UserPromptSubmit", `{"session_id":"c1","prompt":"`+wakeupPrompt("a7")+`"}`)
	hook(5*time.Minute+time.Second, "Stop", `{"session_id":"c1"}`)
	if n := d.notices(); len(n) != 1 || n[0].Kind != "done" {
		t.Errorf("agent: %+v", n)
	}
	if len(d.work().running[sessionID(t, d)]) != 1 {
		t.Error("the command was dropped")
	}

	// Claude exits: what it ran in the background went with it.
	hook(6*time.Minute, "SessionEnd", `{"session_id":"c1","reason":"exit"}`)
	if len(d.work().running) != 0 || len(d.work().since) != 0 {
		t.Errorf("kept: %+v", d.work())
	}
}

func TestBackgroundWorkIsForgottenAfterAWhile(t *testing.T) {
	d, clock, _ := notifyDaemon(t, config.Default().Notify)
	ctx := context.Background()
	t0 := *clock
	hook := func(after time.Duration, name, payload string) {
		t.Helper()
		*clock = t0.Add(after)
		must(t, d.ApplyHook(ctx, hookEvent(name, payload, *clock, ipc.HookContext{})))
	}
	hook(0, "UserPromptSubmit", `{"session_id":"c1","prompt":"watch the logs"}`)
	hook(time.Second, "PostToolUse", `{"session_id":"c1","tool_name":"Bash","tool_response":{"backgroundTaskId":"b9"}}`)
	hook(2*time.Second, "Stop", `{"session_id":"c1"}`)
	// Killed, so it never wakes Claude up: the row stops saying so.
	hook(backgroundKeep+time.Minute, "UserPromptSubmit", `{"session_id":"c1","prompt":"anything else?"}`)
	if len(d.work().running) != 0 {
		t.Errorf("still following: %+v", d.work().running)
	}
}

func sessionID(t *testing.T, d *Daemon) string {
	t.Helper()
	ss, err := d.store.Sessions(context.Background(), true)
	must(t, err)
	if len(ss) != 1 {
		t.Fatalf("sessions: %d", len(ss))
	}
	return ss[0].ID
}
