package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Setenv("ACMD_STATE_DIR", "")
	t.Setenv("ACMD_RUNTIME_DIR", "")
	t.Setenv("ACMD_CONFIG_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if p.StateDir != filepath.Join(home, ".local", "state", "agent-command") {
		t.Errorf("StateDir = %s", p.StateDir)
	}
	wantRuntime := filepath.Join(os.TempDir(), "agent-command-")
	if runtime.GOOS == "linux" {
		wantRuntime = filepath.Join(p.StateDir, "run")
	}
	if !strings.HasPrefix(p.RuntimeDir, wantRuntime) || len(p.Socket()) >= maxSunPath {
		t.Errorf("RuntimeDir = %s (socket %d bytes), want %s…", p.RuntimeDir, len(p.Socket()), wantRuntime)
	}
	if p.Config() != filepath.Join(home, ".config", "agent-command", "config.toml") {
		t.Errorf("Config = %s", p.Config())
	}
	for _, f := range []string{p.Socket(), p.Lock(), p.DB(), p.Spool(), p.DaemonLog(), p.Env(), p.Setup(), p.Backups(), p.RemoteIdentityDir()} {
		if !filepath.IsAbs(f) {
			t.Errorf("%s is not absolute", f)
		}
	}
}

func TestDefaultOverridesAndLongTmpdir(t *testing.T) {
	t.Setenv("ACMD_STATE_DIR", "/x/state")
	t.Setenv("ACMD_RUNTIME_DIR", "/x/run")
	t.Setenv("ACMD_CONFIG_DIR", "/x/config")
	if p, _ := Default(); p.StateDir != "/x/state" || p.RuntimeDir != "/x/run" || p.ConfigDir != "/x/config" {
		t.Errorf("overrides ignored: %+v", p)
	}
	t.Setenv("ACMD_RUNTIME_DIR", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/"+strings.Repeat("d", 120))
	if p, _ := Default(); p.RuntimeDir != "/x/state/run" {
		t.Errorf("long TMPDIR should fall back to the state folder, not /tmp: %s", p.RuntimeDir)
	}
}

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

func TestDefaultRuntimeDir(t *testing.T) {
	long := "/" + strings.Repeat("d", 120)
	for _, tc := range []struct {
		name, goos, xdg, tmp, want string
	}{
		{"macOS uses the user's own TMPDIR", "darwin", "", "/var/folders/ab/T", "/var/folders/ab/T/agent-command-501"},
		{"macOS ignores XDG_RUNTIME_DIR", "darwin", "/run/user/501", "/var/folders/ab/T", "/var/folders/ab/T/agent-command-501"},
		{"macOS with a TMPDIR too long for a socket", "darwin", "", long, "/home/u/state/run"},
		{"Linux uses XDG_RUNTIME_DIR", "linux", "/run/user/501", "/tmp", "/run/user/501/agent-command"},
		{"Linux without XDG_RUNTIME_DIR never uses the shared /tmp", "linux", "", "/tmp", "/home/u/state/run"},
		{"Linux with an XDG_RUNTIME_DIR too long for a socket", "linux", long, "/tmp", "/home/u/state/run"},
	} {
		if got := defaultRuntimeDir(tc.goos, tc.xdg, tc.tmp, "/home/u/state", 501); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
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

func TestEnsureAllowsASymlinkedStateFolder(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Paths{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run")}
	if err := os.Symlink(target, p.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatalf("a state folder the user symlinked: %v", err)
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
