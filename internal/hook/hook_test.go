package hook

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
)

const v = "1.2.3-test"

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r")}
}

// fakeProcs builds a process tree: hook shell (100) -> claude (200, args) -> login (300).
func fakeProcs(claudeArgs ...string) func(int) (Proc, error) {
	tree := map[int]Proc{
		100: {Pid: 100, PPid: 200, Comm: "zsh", Args: []string{"/bin/zsh", "-c", "acmd hook Stop"}},
		200: {Pid: 200, PPid: 300, Comm: "claude", Args: append([]string{"/Users/u/.local/bin/claude"}, claudeArgs...)},
		300: {Pid: 300, PPid: 1, Comm: "login"},
	}
	return func(pid int) (Proc, error) {
		p, ok := tree[pid]
		if !ok {
			return Proc{}, errors.New("no such process")
		}
		return p, nil
	}
}

func opts(p paths.Paths, stdin string, spawned *int) Options {
	return Options{
		Paths: p, Version: v, Stdin: strings.NewReader(stdin), ParentPid: 100, ProcInfo: fakeProcs(),
		Getenv: func(k string) string {
			return map[string]string{"KITTY_WINDOW_ID": "7", "TERM_PROGRAM": "kitty"}[k]
		},
		Now:     func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) },
		Spawn:   func(paths.Paths) error { *spawned++; return nil },
		Timeout: 300 * time.Millisecond,
	}
}

func readSpool(t *testing.T, p paths.Paths) []ipc.HookEvent {
	t.Helper()
	f, err := os.Open(p.Spool())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []ipc.HookEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e ipc.HookEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestRunSpoolsAndSpawnsWithoutDaemon(t *testing.T) {
	p := testPaths(t)
	spawned := 0
	out, err := Run("Stop", opts(p, `{"session_id":"c1"}`, &spawned))
	if out != Spooled || err != nil {
		t.Fatalf("Run = %s, %v", out, err)
	}
	if spawned != 1 {
		t.Errorf("spawned %d daemons, want 1", spawned)
	}
	evs := readSpool(t, p)
	if len(evs) != 1 || evs[0].Name != "Stop" || evs[0].Context.ClaudePid != 200 || evs[0].Context.KittyWindowID != "7" {
		t.Fatalf("spool = %+v", evs)
	}
	st, _ := os.Stat(p.Spool())
	if st.Mode().Perm() != 0o600 {
		t.Errorf("spool permissions %o", st.Mode().Perm())
	}

	// A failing spawn still leaves the event spooled.
	o := opts(p, `{"session_id":"c1"}`, &spawned)
	o.Spawn = func(paths.Paths) error { return errors.New("boom") }
	if out, err := Run("Stop", o); out != Spooled || err == nil {
		t.Errorf("failing spawn: %s, %v", out, err)
	}
	if n := len(readSpool(t, p)); n != 2 {
		t.Errorf("spool has %d events, want 2", n)
	}
}

func TestRunSendsToDaemon(t *testing.T) {
	p := testPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: v, Log: log.New(io.Discard, "", 0)}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
	waitForSocket(t, p)

	spawned := 0
	out, err := Run("SessionStart", opts(p, `{"session_id":"c1","source":"startup"}`, &spawned))
	if out != Sent || err != nil || spawned != 0 {
		t.Fatalf("Run = %s, %v (spawned %d)", out, err, spawned)
	}
	c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: v})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var st ipc.Status
	if err := c.Call(ipc.OpStatus, nil, &st); err != nil || st.Sessions != 1 {
		t.Errorf("status = %+v, %v", st, err)
	}
}

func TestRunReplacesDaemonOfOtherVersion(t *testing.T) {
	p := testPaths(t)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(context.Background(), daemon.Config{Paths: p, Version: "0.0.1-old", Log: log.New(io.Discard, "", 0)})
	}()
	waitForSocket(t, p)

	spawned := 0
	out, err := Run("Stop", opts(p, `{"session_id":"c1"}`, &spawned))
	if out != Spooled || spawned != 1 {
		t.Fatalf("Run = %s, %v (spawned %d)", out, err, spawned)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("old daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old daemon wasn't shut down")
	}
}

func TestRunIgnoresHeadlessAndRejectsBadInput(t *testing.T) {
	p := testPaths(t)
	spawned := 0
	o := opts(p, `{"session_id":"c1"}`, &spawned)
	o.ProcInfo = fakeProcs("-p", "summarize")
	if out, _ := Run("Stop", o); out != Ignored {
		t.Errorf("headless: %s", out)
	}
	if out, err := Run("Stop", opts(p, `not json`, &spawned)); out != Rejected || err == nil {
		t.Errorf("bad json: %s %v", out, err)
	}
	big := `{"x":"` + strings.Repeat("a", 1<<20) + `"}`
	if out, err := Run("Stop", opts(p, big, &spawned)); out != Rejected || err == nil {
		t.Errorf("oversized: %s %v", out, err)
	}
	if spawned != 0 {
		t.Errorf("spawned %d", spawned)
	}
	if _, err := os.Stat(p.Spool()); !errors.Is(err, os.ErrNotExist) {
		t.Error("nothing should be spooled")
	}
}

func TestIsHeadless(t *testing.T) {
	cases := map[string]bool{
		"claude":                       false,
		"claude -p hi":                 true,
		"claude --print hi":            true,
		"claude --print=json":          true,
		"claude --resume abc":          false,
		"claude -- -p":                 false,
		"claude --bg fix -prod config": false,
		"claude -c":                    false,
		"":                             false,
	}
	for cmdline, want := range cases {
		if got := IsHeadless(strings.Fields(cmdline)); got != want {
			t.Errorf("IsHeadless(%q) = %v, want %v", cmdline, got, want)
		}
	}
}

func TestFindClaude(t *testing.T) {
	if p, ok := findClaude(100, fakeProcs()); !ok || p.Pid != 200 {
		t.Errorf("findClaude = %+v %v", p, ok)
	}
	// By argv[0] when comm differs (e.g. node wrapper).
	tree := func(pid int) (Proc, error) {
		if pid == 5 {
			return Proc{Pid: 5, PPid: 1, Comm: "node", Args: []string{"/x/claude"}}, nil
		}
		return Proc{}, errors.New("gone")
	}
	if p, ok := findClaude(5, tree); !ok || p.Pid != 5 {
		t.Errorf("by argv: %+v %v", p, ok)
	}
	if _, ok := findClaude(42, tree); ok {
		t.Error("missing process should not be found")
	}
	if _, ok := findClaude(1, tree); ok {
		t.Error("walk must stop at pid 1")
	}
}

func TestProcInfoRealProcess(t *testing.T) {
	p, err := procInfo(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if p.PPid != os.Getppid() || p.Comm == "" || len(p.Args) == 0 || !strings.HasSuffix(p.Args[0], filepath.Base(os.Args[0])) {
		t.Errorf("procInfo(self) = %+v (os.Args[0]=%s)", p, os.Args[0])
	}
	if _, err := procInfo(999999); err == nil {
		t.Error("expected error for a nonexistent pid")
	}
}

func waitForSocket(t *testing.T, p paths.Paths) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(p.Socket()); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket didn't appear")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
