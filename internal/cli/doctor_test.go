package cli

import (
	"context"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// The daemon's doctor op makes acmd doctor's checks, as structured checks.
func TestDaemonDoctorIsDoctorsChecks(t *testing.T) {
	env(t)
	r := daemonDoctor(context.Background())
	if len(r.Checks) == 0 {
		t.Fatal("no checks")
	}
	sections := map[string]bool{}
	for _, c := range r.Checks {
		sections[c.Section] = true
		switch c.Status {
		case ipc.DoctorOK, ipc.DoctorInfo, ipc.DoctorWarn, ipc.DoctorFail:
		default:
			t.Errorf("status %q", c.Status)
		}
	}
	if !sections["acmd"] {
		t.Errorf("sections = %v", sections)
	}
}
