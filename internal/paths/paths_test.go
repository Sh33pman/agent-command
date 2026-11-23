package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps its folders under LOCALAPPDATA: TestWindowsDefault")
	}
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
	if runtime.GOOS == "windows" {
		return // no TMPDIR there: run\ is always in the state folder
	}
	t.Setenv("ACMD_RUNTIME_DIR", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/"+strings.Repeat("d", 120))
	if p, _ := Default(); p.RuntimeDir != "/x/state/run" {
		t.Errorf("long TMPDIR should fall back to the state folder, not /tmp: %s", p.RuntimeDir)
	}
}

func TestDefaultRuntimeDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS's and Linux's runtime folders, as Unix paths")
	}
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

func TestEnsureAllowsASymlinkedStateFolder(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Paths{StateDir: filepath.Join(dir, "state"), RuntimeDir: filepath.Join(dir, "run")}
	if err := os.Symlink(target, p.StateDir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("can't make a symlink here (needs Developer Mode or admin): %v", err)
		}
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatalf("a state folder the user symlinked: %v", err)
	}
}

func TestWindowsDefault(t *testing.T) {
	t.Setenv("ACMD_STATE_DIR", "")
	t.Setenv("ACMD_RUNTIME_DIR", "")
	t.Setenv("ACMD_CONFIG_DIR", "")
	local := filepath.Join("C:", "Users", "u", "AppData", "Local")
	p, err := windowsDefault(local)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(local, "agent-command")
	want := Paths{StateDir: root, ConfigDir: filepath.Join(root, "config"), RuntimeDir: filepath.Join(root, "run")}
	if p != want {
		t.Errorf("windowsDefault = %+v, want %+v", p, want)
	}
	if p.Socket() != filepath.Join(root, "run", "daemon.sock") {
		t.Errorf("Socket = %s", p.Socket())
	}

	t.Setenv("ACMD_STATE_DIR", "s")
	t.Setenv("ACMD_CONFIG_DIR", "c")
	t.Setenv("ACMD_RUNTIME_DIR", "r")
	if p, _ := windowsDefault(local); p != (Paths{StateDir: "s", ConfigDir: "c", RuntimeDir: "r"}) {
		t.Errorf("overrides ignored: %+v", p)
	}
	if _, err := windowsDefault(""); err == nil {
		t.Error("no LOCALAPPDATA: want an error")
	}
}
