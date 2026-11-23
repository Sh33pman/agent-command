package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/service/servicetest"
	"github.com/brettmostert/agent-command/internal/wake"
)

// TestMain keeps every test away from the Mac's real power schedule and
// sudoers: nothing here may run sudo, pmset or osascript.
func TestMain(m *testing.M) {
	// Nothing here may reach the real acmd: its state, settings, sockets or
	// daemon. Every test's folders are a sandbox's unless it sets its own
	// (env does), and a daemon is started only by a test that means to: a
	// spawned one would run this binary on whatever folders it was given.
	sandbox, err := os.MkdirTemp("", "acmdcli-sandbox")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"ACMD_STATE_DIR": filepath.Join(sandbox, "state"), "ACMD_RUNTIME_DIR": filepath.Join(sandbox, "run"),
		"ACMD_CONFIG_DIR": filepath.Join(sandbox, "config"), "HOME": filepath.Join(sandbox, "home"),
		"CLAUDE_CONFIG_DIR": filepath.Join(sandbox, "home", ".claude"), "XDG_CONFIG_HOME": filepath.Join(sandbox, "xdg"),
	} {
		_ = os.Setenv(k, v)
	}
	spawnDaemon = func(paths.Paths) error {
		return errors.New("a test reached the daemon without env(t): it starts none")
	}
	localWaker = func() wake.Waker { return &wake.Mac{Run: noRun} }
	asRoot = func(context.Context, io.Reader, io.Writer, string, string) error {
		return errors.New("tests don't run anything as root")
	}
	ruleInstalled = func() bool { return false }
	// No service manager, unless a test brings a fake one (withService).
	localService = func() (service.Env, error) {
		return servicetest.New("plan9", os.Getenv("HOME")).Env(), nil
	}
	code := m.Run()
	_ = os.RemoveAll(sandbox)
	os.Exit(code)
}

func noRun(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("tests don't run sudo or pmset")
}
