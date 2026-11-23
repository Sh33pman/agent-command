//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsure(t *testing.T) {
	dir := t.TempDir()
	p := Paths{StateDir: filepath.Join(dir, "a", "state"), RuntimeDir: filepath.Join(dir, "run")}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		st, err := os.Stat(d)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %o", d, err, st.Mode().Perm())
		}
	}
}

func mode(t *testing.T, dir string) os.FileMode {
	t.Helper()
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestEnsureTightensAFolderThatIsTooOpen(t *testing.T) {
	dir := t.TempDir()
	p := Paths{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run")}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if m := mode(t, d); m != 0o700 {
			t.Errorf("%s is %o, want 700", d, m)
		}
	}
}

// Another user can make the runtime folder first when it is in a shared place,
// as a symlink to a folder the victim owns. Ensure must not follow it: not to
// put the sockets there, and not to change that folder's mode.
func TestEnsureRefusesASymlinkedRuntimeFolder(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Paths{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run")}
	if err := os.Symlink(target, p.RuntimeDir); err != nil {
		t.Fatal(err)
	}
	err := p.Ensure()
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Ensure = %v, want it to refuse the symlink", err)
	}
	if m := mode(t, target); m != 0o755 {
		t.Errorf("the symlink's target was changed to %o", m)
	}
}

func TestOwnDirRefusesAnotherUsersFolder(t *testing.T) {
	dir := t.TempDir()
	// The folder is this user's, so asking for any other owner must fail.
	err := ownDir(dir, os.Getuid()+1, true)
	if err == nil || !strings.Contains(err.Error(), "belongs to user") {
		t.Fatalf("ownDir = %v, want it to refuse another user's folder", err)
	}
	if err := ownDir(dir, os.Getuid(), true); err != nil {
		t.Fatalf("the user's own folder: %v", err)
	}
}

func TestOwnDirRefusesAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ownDir(f, os.Getuid(), true); err == nil {
		t.Fatal("a file passed as a folder")
	}
}
