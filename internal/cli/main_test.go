package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/brettmostert/agent-command/internal/wake"
)

// TestMain keeps every test away from the Mac's real power schedule and
// sudoers: nothing here may run sudo, pmset or osascript.
func TestMain(m *testing.M) {
	localWaker = func() wake.Waker { return &wake.Mac{Run: noRun} }
	asRoot = func(context.Context, io.Reader, io.Writer, string, string) error {
		return errors.New("tests don't run anything as root")
	}
	ruleInstalled = func() bool { return false }
	os.Exit(m.Run())
}

func noRun(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("tests don't run sudo or pmset")
}
