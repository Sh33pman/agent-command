package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// acmd harness lists, adds, renames, changes and removes harnesses, and
// acmd new and acmd schedule name them.
func TestHarnessCommands(t *testing.T) {
	env(t)
	t.Chdir(t.TempDir())
	out := mustRun(t, "harness", "list")
	if !strings.Contains(out, "Claude") || !strings.Contains(out, "(standard)") || !strings.Contains(out, "Antigravity") {
		t.Errorf("list:\n%s", out)
	}
	out = mustRun(t, "harness", "add", "claude", "My", "Work", "--arg=--model", "--arg=opus")
	if !strings.Contains(out, "Added My Work (Claude).") || !strings.Contains(out, ".claude-my_work") {
		t.Errorf("add: %q", out)
	}
	_, err := acmd(t, "harness", "add", "claude", "Work2")
	errContains(t, err, "letters")
	_, err = acmd(t, "harness", "add", "claude", "my work")
	errContains(t, err, "already called")

	mustRun(t, "harness", "rename", "my_work", "Job")
	mustRun(t, "harness", "set", "Job", "--command", "/bin/true", "--arg=")
	out = mustRun(t, "--json", "harness", "list")
	var hs ipc.Harnesses
	must(t, json.Unmarshal([]byte(out), &hs))
	if len(hs.Harnesses) != 3 || hs.Harnesses[2].Name != "Job" || hs.Harnesses[2].Command != "/bin/true" || len(hs.Harnesses[2].Args) != 0 ||
		hs.Harnesses[2].ConfigDir != "~/.claude-my_work" {
		t.Errorf("--json list: %s", out)
	}
	_, err = acmd(t, "harness", "set", "Job")
	errContains(t, err, "give --command")
	_, err = acmd(t, "harness", "rename", "claude", "x1")
	errContains(t, err, "letters")
	_, err = acmd(t, "harness", "remove", "nobody", "--yes")
	errContains(t, err, `there's no harness "nobody"`)

	// A job in it holds it; deleting the job lets it go.
	mustRun(t, "schedule", "add", "Nightly", "--cron", "0 2 * * *", "--prompt", "go", "--harness", "job")
	_, err = acmd(t, "harness", "remove", "Job", "--yes")
	errContains(t, err, "runs scheduled jobs")
	if out := mustRun(t, "schedule", "show", "Nightly"); !strings.Contains(out, "Harness   Job (Claude)") {
		t.Errorf("show: %s", out)
	}
	mustRun(t, "schedule", "delete", "Nightly", "--yes")
	if out := mustRun(t, "harness", "remove", "Job", "--yes"); !strings.Contains(out, "Removed Job.") {
		t.Errorf("remove: %q", out)
	}
	out = mustRun(t, "schedule", "settings", "--harness", "antigravity")
	if !strings.Contains(out, "ANTIGRAVITY FLAG") || !strings.Contains(out, "--mode") {
		t.Errorf("settings: %s", out)
	}
}
