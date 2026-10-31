package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

var (
	fakeClaudeOnce sync.Once
	fakeClaudeBin  string
	fakeClaudeErr  error
)

// buildFakeClaude compiles test/fakeclaude once per test binary.
func buildFakeClaude(t *testing.T) string {
	t.Helper()
	fakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeclaude")
		if err != nil {
			fakeClaudeErr = err
			return
		}
		fakeClaudeBin = filepath.Join(dir, "claude")
		if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "../../test/fakeclaude").CombinedOutput(); err != nil {
			fakeClaudeErr = errors.New(string(out))
		}
	})
	if fakeClaudeErr != nil {
		t.Fatal(fakeClaudeErr)
	}
	return fakeClaudeBin
}

type viewEnv struct {
	dir, state, trust string
	conn              *ipc.Conn
}

// viewEnvFor runs a real daemon whose claude is test/fakeclaude.
func viewEnvFor(t *testing.T) *viewEnv {
	t.Helper()
	claude := buildFakeClaude(t)
	dir, err := os.MkdirTemp("", "acmdview")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e := &viewEnv{dir: dir, state: filepath.Join(dir, "fake.json"), trust: filepath.Join(dir, "claude.json")}
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	for k, v := range map[string]string{"ACMD_STATE_DIR": p.StateDir, "ACMD_RUNTIME_DIR": p.RuntimeDir, "ACMD_CONFIG_DIR": p.ConfigDir,
		"ACMD_NO_AUTO_INIT": "1", "ACMD_VIEW_ID": "", "FAKECLAUDE_STATE": e.state, "FAKECLAUDE_TRUST_FILE": e.trust} {
		t.Setenv(k, v)
	}
	must(t, os.MkdirAll(p.ConfigDir, 0o700))
	must(t, os.WriteFile(p.Config(), []byte("claude_command = \""+claude+"\"\nclaude_args = [\"--model\", \"opus\"]\n"), 0o600))
	origSpawn, origTrust := spawnDaemon, trustFile
	spawnDaemon = func(paths.Paths) error { return errors.New("tests start the daemon themselves") }
	trustFile = func() string { return e.trust }
	t.Cleanup(func() { spawnDaemon, trustFile = origSpawn, origTrust })

	cli := claudecli.New(claude, os.Environ())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: version.Get().Version, Log: log.New(io.Discard, "", 0),
			Agents: cli, Stopper: cli, Claude: cli, TailEvery: time.Hour, ReconcileEvery: 50 * time.Millisecond})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e.conn, err = ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { e.conn.Close() })
	return e
}

func (e *viewEnv) snapshot(t *testing.T) ipc.Snapshot {
	t.Helper()
	var s ipc.Snapshot
	must(t, e.conn.Call(ipc.OpSnapshot, nil, &s))
	return s
}

func (e *viewEnv) fakeCalls(t *testing.T) []string {
	t.Helper()
	var st struct{ Calls []string }
	b, _ := os.ReadFile(e.state)
	_ = json.Unmarshal(b, &st)
	return st.Calls
}

func (e *viewEnv) folder(t *testing.T, name string, untrusted bool) string {
	t.Helper()
	d := filepath.Join(e.dir, name)
	must(t, os.MkdirAll(d, 0o755))
	if untrusted {
		must(t, os.WriteFile(filepath.Join(d, ".untrusted"), nil, 0o600))
	}
	return d
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNewGoResume(t *testing.T) {
	e := viewEnvFor(t)
	api := e.folder(t, "api", false)

	out := mustRun(t, "new", "--cwd", api, "--name", "auth work", "--group", "PROJ-1", "--description", "d", "--", "fix", "it")
	if !strings.Contains(out, `Started "auth work" in `) || !strings.Contains(out, "Show it in a view: acmd go ") {
		t.Errorf("new output:\n%s", out)
	}
	if calls := e.fakeCalls(t); !contains(calls, "--bg --name auth work --model opus -- fix it") {
		t.Errorf("claude calls: %q", calls)
	}
	snap := e.snapshot(t)
	var s *core.Session
	for _, x := range snap.Sessions {
		s = x
	}
	g, _ := resolveGroup(snap, "PROJ-1")
	realAPI, _ := filepath.EvalSymlinks(api) // Claude reports resolved folders
	if s == nil || s.Name != "auth work" || s.GroupID != g.ID || (s.Cwd != api && s.Cwd != realAPI) || s.BgID == "" {
		t.Fatalf("session: %+v", s)
	}
	// The reconciler links it to Claude's listing (no hooks in this test).
	eventually(t, "link", func() bool {
		for _, x := range e.snapshot(t).Sessions {
			if x.ID == s.ID && strings.HasPrefix(x.ClaudeSessionID, s.BgID) {
				return true
			}
		}
		return false
	})
	if _, err := acmd(t, "new", "--cwd", api, "--group", "nope", "--no-create"); err == nil {
		t.Error("--no-create with a missing group accepted")
	}
	if _, err := acmd(t, "new", "--cwd", filepath.Join(e.dir, "missing")); err == nil {
		t.Error("missing folder accepted")
	}

	// go: needs a view.
	if _, err := acmd(t, "go", "auth"); err == nil || !strings.Contains(err.Error(), "run `acmd` in a pane") {
		t.Errorf("go without a view: %v", err)
	}
	var older, newer core.View
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "unknown"}}, &older))
	time.Sleep(10 * time.Millisecond)
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "unknown"}}, &newer))
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSelect, ID: newer.ID}, nil))
	mustRun(t, "go", "auth")
	for _, v := range e.snapshot(t).Views {
		if want := map[string]string{older.ID: s.ID, newer.ID: s.ID}[v.ID]; v.Selected != want {
			t.Errorf("view %s shows %q, want %q", v.ID, v.Selected, want)
		}
	}
	// In a view, go and new act on that view.
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSelect, ID: older.ID}, nil))
	t.Setenv("ACMD_VIEW_ID", older.ID)
	mustRun(t, "go", "auth")
	mustRun(t, "new", "--cwd", api, "--name", "second")
	for _, v := range e.snapshot(t).Views {
		if v.ID == older.ID && v.Selected == s.ID {
			t.Error("new inside a view didn't switch it")
		}
	}
	t.Setenv("ACMD_VIEW_ID", "")

	// resume: running sessions are left alone; a stopped one is resumed.
	out = mustRun(t, "resume", "auth")
	if !strings.Contains(out, `"auth work" is already running.`) {
		t.Errorf("resume running:\n%s", out)
	}
	must(t, exec.Command(buildFakeClaude(t), "stop", s.BgID).Run())
	eventually(t, "paused", func() bool {
		for _, x := range e.snapshot(t).Sessions {
			if x.ID == s.ID {
				return x.Status == core.StatusPaused
			}
		}
		return false
	})
	out = mustRun(t, "resume", "--all")
	if !strings.Contains(out, `Resumed "auth work" (claude attach `+s.BgID+").") {
		t.Errorf("resume --all:\n%s", out)
	}
	if !strings.Contains(mustRun(t, "resume", "--all"), "No paused sessions.") {
		t.Error("second resume --all")
	}
	for _, bad := range [][]string{{"resume"}, {"resume", "auth", "--all"}, {"resume", "a", "b"}} {
		if _, err := acmd(t, bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestNewInUntrustedFolder(t *testing.T) {
	e := viewEnvFor(t)
	dir := e.folder(t, "fresh", true)

	// Without a terminal: a clear error, nothing started.
	_, err := acmd(t, "new", "--cwd", dir)
	if err == nil || !strings.Contains(err.Error(), "hasn't trusted") {
		t.Errorf("non-interactive untrusted: %v", err)
	}
	if n := len(e.snapshot(t).Sessions); n != 0 {
		t.Errorf("%d sessions after a refused start", n)
	}

	// In a terminal: Claude's trust prompt runs here; accepting it closes
	// the prompt and the session starts.
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("accept\n"))
	cmd.SetOut(&out)
	s, err := startSession(context.Background(), cmd, newOptions{cwd: dir, interactive: true})
	if err != nil {
		t.Fatalf("trust flow: %v\n%s", err, out.String())
	}
	for _, want := range []string{"Claude hasn't trusted " + dir, "TRUST PROMPT for ", "TRUSTED"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if s.BgID == "" || len(e.snapshot(t).Sessions) != 1 {
		t.Errorf("session after trust: %+v", s)
	}

	// Declining (closing the prompt) starts nothing.
	other := e.folder(t, "other", true)
	cmd.SetIn(strings.NewReader("")) // the user closes the prompt
	if _, err := startSession(context.Background(), cmd, newOptions{cwd: other, interactive: true}); err == nil ||
		!strings.Contains(err.Error(), "still isn't trusted") {
		t.Errorf("declined trust: %v", err)
	}
}

func TestCurrentPaneAndTargetView(t *testing.T) {
	env := map[string]string{"KITTY_WINDOW_ID": "7", "KITTY_LISTEN_ON": "unix:/tmp/k"}
	if p := currentPane(func(k string) string { return env[k] }); p.Terminal != "kitty" || p.KittyWindow != 7 || p.KittySocket != "unix:/tmp/k" {
		t.Errorf("kitty pane: %+v", p)
	}
	if p := currentPane(func(k string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[k] }); p.Terminal != "ghostty" {
		t.Errorf("ghostty pane: %+v", p)
	}
	if p := currentPane(func(string) string { return "" }); p.Terminal != "unknown" {
		t.Errorf("other: %+v", p)
	}
	t0 := time.Now()
	snap := ipc.Snapshot{Views: []*core.View{{ID: "a", LastFocusedAt: t0}, {ID: "b", LastFocusedAt: t0.Add(time.Second)}}}
	if v, _ := targetView(snap, ""); v.ID != "b" {
		t.Errorf("most recent: %s", v.ID)
	}
	if v, _ := targetView(snap, "a"); v.ID != "a" {
		t.Errorf("current: %s", v.ID)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
