package cli

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

func TestDaemonStopRestart(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r")}
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	t.Setenv("ACMD_CONFIG_DIR", filepath.Join(dir, "c"))

	exited := make(chan error, 4)
	start := func(paths.Paths) error {
		go func() {
			exited <- daemon.Run(context.Background(), daemon.Config{Paths: p, Version: version.Get().Version,
				Log: log.New(io.Discard, "", 0), TailEvery: time.Hour, ReconcileEvery: time.Hour})
		}()
		return nil
	}
	orig := spawnDaemon
	spawnDaemon = start
	t.Cleanup(func() { spawnDaemon = orig })

	cmd := func(args ...string) string {
		t.Helper()
		out, err := run(t, "acmd", append([]string{"daemon"}, args...)...)
		if err != nil {
			t.Fatalf("daemon %v: %v\n%s", args, err, out)
		}
		return out
	}
	waitExit := func() {
		t.Helper()
		select {
		case err := <-exited:
			if err != nil {
				t.Fatalf("daemon exited with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("daemon still running")
		}
	}

	if out := cmd("stop"); !strings.Contains(out, "wasn't running") {
		t.Errorf("stop with none running: %q", out)
	}
	if out := cmd("restart"); !strings.Contains(out, "Started") {
		t.Errorf("restart with none running: %q", out)
	}
	if out := cmd("restart"); !strings.Contains(out, "Restarted") {
		t.Errorf("restart: %q", out)
	}
	waitExit() // the first one
	if out := cmd("stop"); !strings.Contains(out, "Stopped") {
		t.Errorf("stop: %q", out)
	}
	waitExit()
}
