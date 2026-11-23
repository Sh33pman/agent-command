package identity

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/brettmostert/agent-command/internal/paths"
)

// notFound is how Windows says a file isn't there.
const notFound = "cannot find the file"

// readableBy lets Everyone read path. Windows has no group bits: "group"
// skips.
func readableBy(t *testing.T, path, who string) {
	t.Helper()
	if who == "group" {
		t.Skip("Windows has no group bits: its access lists are checked with Everyone")
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	must(t, err)
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	must(t, err)
	must(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil))
}

// private checks only you (and SYSTEM and Administrators) can read path.
// Windows has no mode bits to compare with want.
func private(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	if who, _, err := paths.OthersCanRead(path); err != nil || who != "" {
		t.Errorf("%s: others can read it: %q (%v)", path, who, err)
	}
}
