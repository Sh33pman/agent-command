package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func me(t *testing.T) *windows.SID {
	t.Helper()
	sid, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

// grantEveryoneWrite adds an inherited-style entry that lets Everyone write to
// dir, as a folder made somewhere shared might have.
func grantEveryoneWrite(t *testing.T, dir string) {
	t.Helper()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLeavesOnlyTheUserAbleToWrite(t *testing.T) {
	dir := t.TempDir()
	p := Paths{StateDir: filepath.Join(dir, "a", "state"), RuntimeDir: filepath.Join(dir, "a", "state", "run")}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if who, err := othersWhoCanWrite(d, me(t)); err != nil || len(who) > 0 {
			t.Errorf("%s: others who can write %v (%v)", d, who, err)
		}
		if ok, err := onlyFor(d, me(t)); !ok || err != nil {
			t.Errorf("%s isn't the user's alone (%v)", d, err)
		}
	}
	// What's made inside inherits it: a socket's folder entry, a key.
	f := filepath.Join(p.StateDir, "key.pem")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if who, err := othersWhoCanWrite(f, me(t)); err != nil || len(who) > 0 {
		t.Errorf("a file in it: others who can write %v (%v)", who, err)
	}
}

func TestEnsureTakesAwayOthersWrite(t *testing.T) {
	dir := t.TempDir()
	p := Paths{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run")}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		grantEveryoneWrite(t, d)
		if who, _ := othersWhoCanWrite(d, me(t)); len(who) == 0 {
			t.Fatalf("%s: the test's Everyone entry isn't there", d)
		}
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if who, err := othersWhoCanWrite(d, me(t)); err != nil || len(who) > 0 {
			t.Errorf("%s: others can still write %v (%v)", d, who, err)
		}
	}
}

func TestEnsureRefusesASymlinkedRuntimeFolderOnWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Paths{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run")}
	if err := os.Symlink(target, p.RuntimeDir); err != nil {
		t.Skipf("can't make a symlink here (needs Developer Mode or admin): %v", err)
	}
	if err := p.Ensure(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Ensure = %v, want it to refuse the symlink", err)
	}
}

func TestOwnDirRefusesAFileOnWindows(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ownDir(f, me(t), true); err == nil {
		t.Fatal("a file passed as a folder")
	}
}

func TestWritersOtherThanANilListIsEveryone(t *testing.T) {
	if who, err := writersOtherThan(nil, me(t)); err != nil || len(who) != 1 {
		t.Errorf("a nil access list: %v (%v)", who, err)
	}
}

func TestOthersCanReadFollowsTheAccessList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "id")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A temp folder's usual list (you, SYSTEM, Administrators) is private.
	if who, _, err := OthersCanRead(key); err != nil || who != "" {
		t.Fatalf("a key in a temp folder: readers %q (%v)", who, err)
	}
	grantEveryoneWrite(t, key)
	who, fix, err := OthersCanRead(key)
	if err != nil || who == "" || !strings.Contains(fix, "icacls") {
		t.Fatalf("Everyone can read it: who %q, fix %q (%v)", who, fix, err)
	}
	// Making its folder private takes Everyone away from the key too, once it
	// inherits again.
	if err := MakePrivate(dir); err != nil {
		t.Fatal(err)
	}
	if who, _, _ := OthersCanRead(dir); who != "" {
		t.Errorf("the folder made private: readers %q", who)
	}
}
