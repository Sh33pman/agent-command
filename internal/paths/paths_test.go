package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Setenv("ACMD_STATE_DIR", "")
	t.Setenv("ACMD_RUNTIME_DIR", "")
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if p.StateDir != filepath.Join(home, ".local", "state", "agent-command") {
		t.Errorf("StateDir = %s", p.StateDir)
	}
	if !strings.Contains(p.RuntimeDir, "agent-command-") || len(p.Socket()) >= maxSunPath {
		t.Errorf("RuntimeDir = %s (socket %d bytes)", p.RuntimeDir, len(p.Socket()))
	}
	for _, f := range []string{p.Socket(), p.Lock(), p.DB(), p.Spool(), p.DaemonLog()} {
		if !filepath.IsAbs(f) {
			t.Errorf("%s is not absolute", f)
		}
	}
}

func TestDefaultOverridesAndLongTmpdir(t *testing.T) {
	t.Setenv("ACMD_STATE_DIR", "/x/state")
	t.Setenv("ACMD_RUNTIME_DIR", "/x/run")
	if p, _ := Default(); p.StateDir != "/x/state" || p.RuntimeDir != "/x/run" {
		t.Errorf("overrides ignored: %+v", p)
	}
	t.Setenv("ACMD_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/"+strings.Repeat("d", 120))
	if p, _ := Default(); !strings.HasPrefix(p.RuntimeDir, "/tmp/agent-command-") {
		t.Errorf("long TMPDIR should fall back to /tmp: %s", p.RuntimeDir)
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
