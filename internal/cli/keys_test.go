package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const keysJSON = `{
  "fixed": [{"keys": "1-9", "section": "Sidebar", "title": "Show that session"}],
  "sections": [
    {"title": "App", "keys": [
      {"action": "app.inbox", "changed": true, "keys": ["⇧⌘K"], "note": "", "title": "Inbox"},
      {"action": "app.rename", "changed": false, "keys": [], "note": "In Scheduled: edits the job.", "title": "Rename"}
    ]},
    {"title": "Lists", "keys": [
      {"action": "list.up", "changed": false, "keys": ["↑", "⌃K", "k"], "note": "", "title": "Move up"}
    ]}
  ]
}`

func TestKeysListsTheAppsShortcuts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", dir)
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))

	if _, err := acmd(t, "keys"); err == nil || !strings.Contains(err.Error(), "open Agent Command once") {
		t.Fatalf("before the app wrote them: %v", err)
	}
	must(t, os.MkdirAll(filepath.Join(dir, "s"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "s", "keys.json"), []byte(keysJSON), 0o600))

	text := mustRun(t, "keys")
	for _, want := range []string{"App\n", "⇧⌘K", "Inbox  (changed)", "(none)", "↑ ⌃K k", "Can't be changed", "1-9", "(Sidebar)"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	md := mustRun(t, "keys", "--markdown")
	for _, want := range []string{"# Agent Command keyboard shortcuts", "## Lists", "| ⇧⌘K | Inbox (changed) |",
		"| (none) | Rename — In Scheduled: edits the job. |", "## Can't be changed"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if out := mustRun(t, "keys", "--json"); out != keysJSON {
		t.Errorf("json: %s", out)
	}
	if _, err := acmd(t, "keys", "--format", "yaml"); ExitCode(err) != exitUsage {
		t.Errorf("--format is gone: %v", err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "s", "keys.json"), []byte("{"), 0o600))
	if _, err := acmd(t, "keys"); err == nil {
		t.Error("a broken file read")
	}
}
