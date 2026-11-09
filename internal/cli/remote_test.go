package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
)

func TestRemoteControlCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", dir)
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	file := filepath.Join(dir, "config.toml")
	load := func() config.Config {
		t.Helper()
		c, err := config.Load(file)
		must(t, err)
		return c
	}

	out, err := acmd(t, "remote-control")
	must(t, err)
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("default: %q, want off", out)
	}

	_, err = acmd(t, "remote-control", "on")
	must(t, err)
	if !load().RemoteControl {
		t.Fatal("on wasn't saved")
	}
	out, err = acmd(t, "remote-control")
	must(t, err)
	if strings.TrimSpace(out) != "on" {
		t.Fatalf("after on: %q", out)
	}

	// Turning it off keeps the user's other settings and comments.
	b, err := os.ReadFile(file)
	must(t, err)
	must(t, os.WriteFile(file, append(b, []byte("# mine\n")...), 0o600))
	_, err = acmd(t, "remote-control", "off")
	must(t, err)
	if load().RemoteControl {
		t.Fatal("off wasn't saved")
	}
	b, err = os.ReadFile(file)
	must(t, err)
	if !strings.Contains(string(b), "# mine") || !strings.Contains(string(b), "[speech]") {
		t.Errorf("the rest of the file changed:\n%s", b)
	}

	if _, err := acmd(t, "remote-control", "maybe"); err == nil {
		t.Error("maybe accepted")
	}
}
