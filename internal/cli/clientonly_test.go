package cli

import (
	"runtime"
	"testing"
)

// sessionsHere skips a test of what runs sessions on this machine: their
// hosts (a pty), the agents' hooks, speech, the editor, waking and the
// service. Windows is client-only; TestInitOnAClientOnlyMachine covers its
// init.
func sessionsHere(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runs or sets up sessions here; Windows is client-only")
	}
}
