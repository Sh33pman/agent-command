package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

type stopRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (s *stopRecorder) Stop(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
	return nil
}

// agents plays `claude agents --json`: the two background sessions are
// running with short ids, so archiving them must call claude stop.
type agents struct{}

func (agents) Agents(context.Context) ([]claudecli.Agent, error) {
	busy, waiting, pid := "busy", "waiting", 4242
	return []claudecli.Agent{
		{SessionID: "c-auth", ID: "aaaaaaaa", Kind: "background", Status: &busy, Pid: &pid, Cwd: "/w/api-server"},
		{SessionID: "c-flaky", ID: "ffffffff", Kind: "background", Status: &waiting, Pid: &pid, Cwd: "/w/web"},
	}, nil
}

func (s *stopRecorder) stopped() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ids...)
}

// env starts a real daemon in temp dirs (via ACMD_* overrides) and seeds
// three sessions through hook events, like Claude would.
func env(t *testing.T) *stopRecorder {
	t.Helper()
	origVisible := visibleSessions
	visibleSessions = func(sessions []*core.Session) []*core.Session { return sessions }
	t.Cleanup(func() { visibleSessions = origVisible })
	dir, err := os.MkdirTemp("", "acmdcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r")}
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	orig := spawnDaemon
	spawnDaemon = func(paths.Paths) error { return errors.New("tests start the daemon themselves") }
	t.Cleanup(func() { spawnDaemon = orig })

	rec := &stopRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: version.Get().Version,
			Log: log.New(io.Discard, "", 0), Stopper: rec, Agents: agents{}, TailEvery: time.Hour, ReconcileEvery: 50 * time.Millisecond,
			Waker: envWaker})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})

	var c *ipc.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err = ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer c.Close()
	t0 := time.Now().Add(-time.Hour)
	send := func(name, payload string, offset time.Duration, hc ipc.HookContext) {
		t.Helper()
		if err := c.Call(ipc.OpEvent, ipc.HookEvent{Name: name, At: t0.Add(offset), Payload: json.RawMessage(payload), Context: hc}, nil); err != nil {
			t.Fatal(err)
		}
	}
	bg := ipc.HookContext{}
	send("SessionStart", `{"session_id":"c-auth","source":"startup","cwd":"/w/api-server"}`, 0, bg)
	send("UserPromptSubmit", `{"session_id":"c-auth","prompt":"refactor auth middleware"}`, time.Second, bg)
	send("SessionStart", `{"session_id":"c-flaky","source":"startup","cwd":"/w/web"}`, 2*time.Second, bg)
	send("UserPromptSubmit", `{"session_id":"c-flaky","prompt":"fix flaky e2e test"}`, 3*time.Second, bg)
	send("PermissionRequest", `{"session_id":"c-flaky","tool_name":"AskUserQuestion"}`, 4*time.Second, bg)
	send("SessionStart", `{"session_id":"c-docs","source":"startup","cwd":"/w/cli-tool"}`, 5*time.Second,
		ipc.HookContext{})
	send("UserPromptSubmit", `{"session_id":"c-docs","prompt":"docs: hooks reference"}`, 6*time.Second,
		ipc.HookContext{})
	send("Stop", `{"session_id":"c-docs"}`, 7*time.Second, ipc.HookContext{})
	return rec
}

func acmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCmd("acmd", &out, &errOut)
	root.SetArgs(args)
	root.SetIn(strings.NewReader(""))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := acmd(t, args...)
	if err != nil {
		t.Fatalf("acmd %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func listJSON(t *testing.T, args ...string) []jsonRow {
	t.Helper()
	var rows []jsonRow
	if err := json.Unmarshal([]byte(mustRun(t, append([]string{"list", "--json"}, args...)...)), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func names(rows []jsonRow) string {
	var n []string
	for _, r := range rows {
		n = append(n, r.Name)
	}
	return strings.Join(n, " | ")
}

func TestListCommand(t *testing.T) {
	env(t)
	out := mustRun(t, "list")
	for _, want := range []string{"#", "STATUS", "WHERE", "refactor auth middleware", "busy", "waiting", "api-server", "bg", "Default"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	rows := listJSON(t)
	if names(rows) != "refactor auth middleware | fix flaky e2e test | docs: hooks reference" || rows[0].Number != 1 {
		t.Errorf("default order: %s", names(rows))
	}
	if rows[1].Status != "waiting" || !rows[1].Unseen {
		t.Errorf("rows: %+v", rows)
	}
	if got := names(listJSON(t, "--status", "waiting,busy")); got != "refactor auth middleware | fix flaky e2e test" {
		t.Errorf("--status: %s", got)
	}
	if got := names(listJSON(t, "--sort", "name")); got != "docs: hooks reference | fix flaky e2e test | refactor auth middleware" {
		t.Errorf("--sort name: %s", got)
	}
	if got := names(listJSON(t, "--sort", "name", "--desc")); !strings.HasPrefix(got, "refactor") {
		t.Errorf("--desc: %s", got)
	}
	if got := names(listJSON(t, "--filter", "flaky")); got != "fix flaky e2e test" {
		t.Errorf("--filter: %s", got)
	}
	if got := listJSON(t, "--group", "nope"); len(got) != 0 {
		t.Errorf("--group unknown: %v", got)
	}
}

func TestRenameDescribeAndSelectors(t *testing.T) {
	env(t)
	mustRun(t, "rename", "2", "flaky", "e2e", "(PROJ-9)")
	mustRun(t, "describe", "refactor", "PROJ-123 follow-up")
	rows := listJSON(t)
	if rows[1].Name != "flaky e2e (PROJ-9)" || rows[0].Description != "PROJ-123 follow-up" {
		t.Errorf("rows: %+v", rows)
	}
	if _, err := acmd(t, "rename", "zzz", "x"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("unknown selector: %v", err)
	}
	mustRun(t, "rename", "1", "fix one")
	if _, err := acmd(t, "rename", "f", "x"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if _, err := acmd(t, "rename", "1", "   "); err == nil {
		t.Error("empty name should fail")
	}
}

func TestGroupsMoveViewAndFocus(t *testing.T) {
	env(t)
	if out := mustRun(t, "group", "create", "PROJ-123 auth"); !strings.Contains(out, `Created group "PROJ-123 auth" (blue)`) {
		t.Errorf("create: %s", out)
	}
	if _, err := acmd(t, "group", "create", "proj-123 AUTH"); err == nil {
		t.Error("duplicate group should fail")
	}
	mustRun(t, "move", "1", "2", "proj-123 auth")
	if _, err := acmd(t, "move", "3", "billing"); err == nil || !strings.Contains(err.Error(), "--create") {
		t.Errorf("move to missing group: %v", err)
	}
	mustRun(t, "move", "3", "billing", "--create")

	rows := listJSON(t)
	if rows[0].Group != "PROJ-123 auth" || rows[2].Group != "billing" {
		t.Errorf("groups: %+v", rows)
	}
	out := mustRun(t, "group", "list")
	if !strings.Contains(out, "PROJ-123 auth") || !strings.Contains(out, "billing") || strings.Index(out, "Default") < strings.Index(out, "billing") {
		t.Errorf("group list (Default last):\n%s", out)
	}
	mustRun(t, "group", "rename", "billing", "billing v2")
	mustRun(t, "group", "color", "billing v2", "green")
	if out := mustRun(t, "group", "list"); !strings.Contains(out, "billing v2") || !strings.Contains(out, "green") {
		t.Errorf("rename/color:\n%s", out)
	}

	// Collapsed groups: sessions still listed, but unnumbered.
	mustRun(t, "group", "collapse", "PROJ-123 auth")
	rows = listJSON(t)
	if rows[0].Number != 0 || rows[2].Number != 1 {
		t.Errorf("collapsed numbering: %+v", rows)
	}
	mustRun(t, "group", "expand", "--all")
	if _, err := acmd(t, "group", "collapse"); err == nil {
		t.Error("collapse needs a group or --all")
	}

	// Focus mode collapses the others and restores them after.
	mustRun(t, "focus-group", "billing v2")
	if rows = listJSON(t); rows[0].Number != 0 || rows[2].Number != 1 {
		t.Errorf("focus: %+v", rows)
	}
	mustRun(t, "focus-group", "--clear")
	if rows = listJSON(t); rows[0].Number != 1 {
		t.Errorf("after clear: %+v", rows)
	}

	mustRun(t, "view-mode", "list")
	mustRun(t, "sort", "name", "--desc")
	if got := names(listJSON(t)); !strings.HasPrefix(got, "refactor") {
		t.Errorf("persisted sort: %s", got)
	}
	if _, err := acmd(t, "sort", "bogus"); err == nil {
		t.Error("bad sort key should fail")
	}
	if _, err := acmd(t, "view-mode", "tree"); err == nil {
		t.Error("bad view mode should fail")
	}

	// Deleting a group moves its sessions to Default.
	if _, err := acmd(t, "group", "delete", "billing v2"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("delete without confirmation: %v", err)
	}
	mustRun(t, "group", "delete", "billing v2", "--yes")
	for _, r := range listJSON(t) {
		if r.Name == "docs: hooks reference" && r.Group != "Default" {
			t.Errorf("after delete: %+v", r)
		}
	}
	if _, err := acmd(t, "group", "delete", "default", "--yes"); err == nil {
		t.Error("deleting Default should fail")
	}
}

func TestArchive(t *testing.T) {
	rec := env(t)
	// Wait for the reconciler to give the background sessions short ids.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows := listJSON(t)
		if rows[0].BgID == "aaaaaaaa" && rows[1].BgID == "ffffffff" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconciler never set short ids: %+v", rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := acmd(t, "archive", "1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("archive without confirmation: %v", err)
	}
	mustRun(t, "archive", "docs", "--yes") // external: never stopped/killed
	if got := rec.stopped(); len(got) != 0 {
		t.Errorf("external session was stopped: %v", got)
	}
	if got := names(listJSON(t)); strings.Contains(got, "docs") {
		t.Errorf("archived still listed: %s", got)
	}
	if got := names(listJSON(t, "--all")); !strings.Contains(got, "docs") {
		t.Errorf("--all should show archived: %s", got)
	}
	mustRun(t, "unarchive", "docs")
	for _, r := range listJSON(t) {
		if r.Name == "docs: hooks reference" && r.Status != string(core.StatusPaused) {
			t.Errorf("unarchived status: %s", r.Status)
		}
	}

	// Group archive archives its sessions and stops the running background ones.
	mustRun(t, "move", "refactor", "fix", "PROJ-1", "--create")
	mustRun(t, "group", "archive", "PROJ-1", "--yes")
	if got := names(listJSON(t)); got != "docs: hooks reference" {
		t.Errorf("after group archive: %s", got)
	}
	if got := strings.Join(rec.stopped(), ","); got != "aaaaaaaa,ffffffff" {
		t.Errorf("claude stop calls = %q, want both background sessions", got)
	}
	if _, err := acmd(t, "group", "archive", "default", "--yes"); err == nil {
		t.Error("archiving Default should fail")
	}
	mustRun(t, "group", "unarchive", "PROJ-1", "--sessions")
	if got := len(listJSON(t)); got != 3 {
		t.Errorf("after group unarchive --sessions: %d sessions", got)
	}
	if _, err := acmd(t, "group", "unarchive", "nope"); err == nil {
		t.Error("unarchive of a missing group should fail")
	}
}

func TestConfirmAndHelpers(t *testing.T) {
	if err := confirm(strings.NewReader(""), io.Discard, true, "q"); err != nil {
		t.Errorf("--yes: %v", err)
	}
	if err := confirm(strings.NewReader("y\n"), io.Discard, false, "q"); err == nil {
		t.Error("non-terminal input must require --yes")
	}
	now = func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	base := now()
	for d, want := range map[time.Duration]string{5 * time.Second: "5s", 3 * time.Minute: "3m", 2 * time.Hour: "2h", 50 * time.Hour: "2d"} {
		if got := age(&core.Session{LastEventAt: base.Add(-d)}); got != want {
			t.Errorf("age(%v) = %s, want %s", d, got, want)
		}
	}
	if age(&core.Session{}) != "-" || age(&core.Session{CreatedAt: base.Add(-time.Minute)}) != "1m" {
		t.Error("age fallbacks")
	}
	if display("", 10) != "(unnamed)" || display("abcdefghijkl", 5) != "abcd…" {
		t.Error("display")
	}
	if where(&core.Session{Kind: core.KindExternal}) != "unknown ↗" {
		t.Error("where unknown")
	}
	var buf bytes.Buffer
	if err := writeListTable(&buf, nil); err != nil || !strings.Contains(buf.String(), "No sessions") {
		t.Errorf("empty table: %q %v", buf.String(), err)
	}
}

func TestPurge(t *testing.T) {
	env(t)
	if out := mustRun(t, "purge", "--yes"); !strings.Contains(out, "No archived sessions to purge.") {
		t.Errorf("nothing archived:\n%s", out)
	}
	mustRun(t, "archive", "docs", "--yes")
	if out := mustRun(t, "purge", "--older-than", "30d", "--yes"); !strings.Contains(out, "No archived sessions") {
		t.Errorf("recently archived purged:\n%s", out)
	}
	if _, err := acmd(t, "purge"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("purge without confirmation: %v", err)
	}
	if out := mustRun(t, "purge", "--yes"); !strings.Contains(out, "Purged 1 session(s).") {
		t.Errorf("purge:\n%s", out)
	}
	if rows := listJSON(t, "--all"); len(rows) != 2 {
		t.Errorf("%d sessions left", len(rows))
	}
	for _, bad := range []string{"x", "-1d", "-5h"} {
		if _, err := acmd(t, "purge", "--older-than", bad, "--yes"); err == nil {
			t.Errorf("--older-than %s accepted", bad)
		}
	}
	if d, err := parseAge("12h"); err != nil || d != 12*time.Hour {
		t.Errorf("12h: %v %v", d, err)
	}
}

// Commands show what the app shows, not rows left over from v1.
func TestHostedSessions(t *testing.T) {
	hosted := &core.Session{ID: "h", Kind: core.KindHosted}
	got := hostedSessions([]*core.Session{{ID: "e", Kind: core.KindExternal}, hosted, {ID: "b", Kind: core.KindBackground}})
	if len(got) != 1 || got[0] != hosted {
		t.Errorf("hosted: %+v", got)
	}
}
