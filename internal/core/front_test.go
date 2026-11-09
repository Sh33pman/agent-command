package core

import "testing"

func TestForegroundFallback(t *testing.T) {
	shell := Event{Kind: EventForeground, Front: FrontShell, Time: at(10)}
	claude := Event{Kind: EventForeground, Front: "claude", Time: at(10)}
	vim := Event{Kind: EventForeground, Front: "vim", Time: at(10)}
	tests := []struct {
		name       string
		harness    string
		from       Status
		claudeID   string
		event      Event
		want       Status
		wantDetail string
		changed    bool
	}{
		{"agent with no hooks is unknown", HarnessTerminal, StatusRunning, "", claude, StatusUnknown, "claude", true},
		{"other program stays running", HarnessTerminal, StatusRunning, "", vim, StatusRunning, "", false},
		{"agent with hooks keeps its status", HarnessTerminal, StatusBusy, "c1", claude, StatusBusy, "", false},
		{"shell back: unknown agent gone", HarnessTerminal, StatusUnknown, "", shell, StatusRunning, "", true},
		{"shell back: agent gone with no SessionEnd", HarnessTerminal, StatusBusy, "c1", shell, StatusRunning, "", true},
		{"shell back: waiting agent gone", HarnessTerminal, StatusWaiting, "c1", shell, StatusRunning, "", true},
		{"shell in front of a plain terminal", HarnessTerminal, StatusRunning, "", shell, StatusRunning, "", false},
		{"paused terminal untouched", HarnessTerminal, StatusPaused, "", shell, StatusPaused, "", false},
		{"not a terminal", HarnessClaude, StatusDone, "c1", shell, StatusDone, "", false},
		{"not a terminal, agent in front", HarnessClaude, StatusDone, "c1", claude, StatusDone, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Harness: tc.harness, Status: tc.from, ClaudeSessionID: tc.claudeID, LastEventAt: at(5)}
			tr := Apply(s, tc.event)
			if s.Status != tc.want || s.StatusDetail != tc.wantDetail || tr.Changed() != tc.changed {
				t.Fatalf("got %s %q changed=%v, want %s %q changed=%v", s.Status, s.StatusDetail, tr.Changed(), tc.want, tc.wantDetail, tc.changed)
			}
			if s.LastEventAt != at(5) {
				t.Fatalf("LastEventAt moved to %v: a hook sent just before would look stale", s.LastEventAt)
			}
		})
	}
}

func TestShellBackForgetsTheAgent(t *testing.T) {
	s := &Session{Harness: HarnessTerminal, Status: StatusDone, ClaudeSessionID: "c1", TranscriptPath: "/t", Pid: 7,
		Name: "fix the bug", NameSource: NameAuto}
	Apply(s, Event{Kind: EventForeground, Front: FrontShell, Time: at(1)})
	if s.ClaudeSessionID != "" || s.TranscriptPath != "" || s.Pid != 0 || s.Name != "" {
		t.Fatalf("the agent's details stayed: %+v", s)
	}
	// Its next agent's hooks are taken.
	e := Event{Kind: HookSessionStart, SessionID: "c2", Source: "startup", Time: at(2)}
	if !s.TakeAgentEvent(e, 9) {
		t.Fatal("the next agent's SessionStart was refused")
	}
}

func TestHooksReplaceUnknown(t *testing.T) {
	s := &Session{Harness: HarnessTerminal, Status: StatusRunning, LastEventAt: at(5)}
	Apply(s, Event{Kind: EventForeground, Front: "claude", Time: at(20)})
	// A hook sent before the host was asked still counts.
	e := Event{Kind: HookUserPromptSubmit, SessionID: "c1", Time: at(19)}
	if !s.TakeAgentEvent(e, 9) {
		t.Fatal("hook refused")
	}
	Apply(s, e)
	if s.Status != StatusBusy {
		t.Fatalf("status %s, want busy", s.Status)
	}
}

func TestShellBackClosesTheInboxItem(t *testing.T) {
	s := &Session{Harness: HarnessTerminal}
	if _, drop := InboxEffect(s, Event{Kind: EventForeground, Front: FrontShell}); !drop {
		t.Fatal("item kept after the agent went")
	}
	if _, drop := InboxEffect(s, Event{Kind: EventForeground, Front: "claude"}); drop {
		t.Fatal("item dropped while the agent runs")
	}
}

func TestIsAgent(t *testing.T) {
	for name, want := range map[string]bool{"claude": true, "opencode": true, "codex": true, "2.1.319": false, "vim": false, "": false, FrontShell: false} {
		if IsAgent(name) != want {
			t.Errorf("IsAgent(%q) = %v", name, !want)
		}
	}
}
