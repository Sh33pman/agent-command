package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
)

// acmdErr runs acmd and returns its stdout and stderr.
func acmdErr(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCmd("acmd", &out, &errOut)
	root.SetArgs(args)
	root.SetIn(strings.NewReader(""))
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestAgentRemoteControlCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		note bool // prints the deprecation note
	}{
		{"agent-remote-control", false},
		{"remote-control", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

			out, errOut, err := acmdErr(t, tc.name)
			must(t, err)
			if strings.TrimSpace(out) != "off" {
				t.Fatalf("default: %q, want off", out)
			}
			if got := strings.Contains(errOut, "acmd agent-remote-control"); got != tc.note {
				t.Errorf("deprecation note = %v, want %v: %q", got, tc.note, errOut)
			}

			out, _, err = acmdErr(t, tc.name, "on")
			must(t, err)
			if !load().AgentRemoteControl || !strings.Contains(out, "Agent Remote Control is on") {
				t.Fatalf("on wasn't saved: %q", out)
			}
			out, _, err = acmdErr(t, tc.name)
			must(t, err)
			if strings.TrimSpace(out) != "on" {
				t.Fatalf("after on: %q", out)
			}

			// Turning it off keeps the user's other settings and comments.
			b, err := os.ReadFile(file)
			must(t, err)
			must(t, os.WriteFile(file, append(b, []byte("# mine\n")...), 0o600))
			_, _, err = acmdErr(t, tc.name, "off")
			must(t, err)
			if load().AgentRemoteControl {
				t.Fatal("off wasn't saved")
			}
			b, err = os.ReadFile(file)
			must(t, err)
			if !strings.Contains(string(b), "# mine") || !strings.Contains(string(b), "[speech]") {
				t.Errorf("the rest of the file changed:\n%s", b)
			}

			if _, _, err := acmdErr(t, tc.name, "maybe"); err == nil {
				t.Error("maybe accepted")
			}
		})
	}
}

// An old config's remote_control is read, and renamed when a setting is next saved.
func TestAgentRemoteControlMigrates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", dir)
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	file := filepath.Join(dir, "config.toml")
	must(t, os.WriteFile(file, []byte("# mine\nremote_control = true # kept\n\n[notify]\ncooldown = \"5s\"\n"), 0o600))

	if out := mustRun(t, "agent-remote-control"); strings.TrimSpace(out) != "on" {
		t.Fatalf("old key: %q, want on", out)
	}
	mustRun(t, "config", "set", "notify.cooldown", "6s")
	b, err := os.ReadFile(file)
	must(t, err)
	want := "# mine\nagent_remote_control = true # kept\n\n[notify]\ncooldown = \"6s\"\n"
	if string(b) != want {
		t.Errorf("after a save:\n%s\nwant:\n%s", b, want)
	}

	// The old name still works with acmd config set.
	mustRun(t, "config", "set", "remote_control", "off")
	if c, _ := config.Load(file); c.AgentRemoteControl {
		t.Error("config set remote_control off didn't turn it off")
	}
}

func TestRemoteNameIsFree(t *testing.T) {
	root := NewRootCmd("acmd", &bytes.Buffer{}, &bytes.Buffer{})
	for _, c := range root.Commands() {
		if c.Name() == "remote" {
			t.Fatal("acmd remote is left for Remote sessions")
		}
		if c.Name() == "remote-control" && !c.Hidden {
			t.Error("acmd remote-control should be hidden")
		}
	}
}
