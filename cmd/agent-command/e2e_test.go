package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

// TestHookSpawnsDaemonEndToEnd runs the real binary the way Claude Code
// does: `acmd hook <Event>` with the payload on stdin and Claude's
// environment. With no daemon running, the hook must spool the event,
// start a detached daemon (re-exec) with CLAUDE* variables stripped (M0
// C5), print nothing, and exit 0; the daemon then applies the event.
func TestHookSpawnsDaemonEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir, err := os.MkdirTemp("", "acmde2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin := filepath.Join(dir, "agent-command")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r")}
	env := append(os.Environ(),
		"ACMD_STATE_DIR="+p.StateDir, "ACMD_RUNTIME_DIR="+p.RuntimeDir, "ACMD_SESSION_ID=e2e",
		"CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ID=parent",
	)

	// A set-up machine: without this, a hook leaves the daemon stopped.
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Setup(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	hook := exec.Command(bin, "hook", "SessionStart")
	hook.Env = env
	hook.Stdin = strings.NewReader(`{"session_id":"e2e-1","hook_event_name":"SessionStart","source":"startup","cwd":"/w"}`)
	var stdout, stderr bytes.Buffer
	hook.Stdout, hook.Stderr = &stdout, &stderr
	start := time.Now()
	if err := hook.Run(); err != nil {
		t.Fatalf("hook exited with %v (stderr %q)", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("hook wrote to stdout: %q", stdout.String())
	}
	// Hang guard only: under a parallel -race test run this can take
	// seconds. Real latency (measured): ~330 ms for the cold path (spool +
	// spawn), ~9 ms median with a daemon running (p99 < 50 ms).
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("hook took %v", d)
	}

	// The spawned daemon drains the spool.
	var c *ipc.Conn
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err = ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: daemonVersion(t, bin)})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(p.DaemonLog())
			t.Fatalf("daemon never came up: %v\ndaemon.log:\n%s", err, log)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() {
		_ = c.Call(ipc.OpShutdown, nil, nil)
		c.Close()
	}()
	var st ipc.Status
	deadline = time.Now().Add(5 * time.Second)
	for {
		if err := c.Call(ipc.OpStatus, nil, &st); err != nil {
			t.Fatal(err)
		}
		if st.Sessions == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.Sessions != 1 || st.Events != 1 {
		t.Fatalf("daemon status = %+v, want the spooled session", st)
	}

	// The daemon must not have inherited Claude's session variables.
	out, err := exec.Command("ps", "-wwE", "-p", strconv.Itoa(st.Pid), "-o", "command=").Output()
	if err != nil {
		t.Skipf("can't read daemon environment: %v", err)
	}
	if !strings.Contains(string(out), "ACMD_STATE_DIR=") {
		t.Skipf("ps doesn't show the environment here: %q", out)
	}
	if strings.Contains(string(out), "CLAUDE") {
		t.Errorf("daemon environment contains CLAUDE variables: %s", out)
	}
}

func daemonVersion(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin, "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var info version.Info
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatal(err)
	}
	return info.Version
}
