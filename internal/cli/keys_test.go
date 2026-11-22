package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
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
	f := newFakeDaemon(t)
	f.fail(ipc.OpKeys, "no app has listed its shortcuts yet: open Agent Command once, then try again")
	if _, err := acmd(t, "keys"); err == nil || !strings.Contains(err.Error(), "open Agent Command once") {
		t.Fatalf("before the app registered them: %v", err)
	}
	answer := ipc.Keymap{ClientID: "mac-1", Kind: ipc.KindMac, Device: "studio", Map: json.RawMessage(keysJSON)}
	f.reply(ipc.OpKeys, answer)

	text := mustRun(t, "keys")
	for _, want := range []string{"App\n", "⇧⌘K", "Inbox  (changed)", "(none)", "↑ ⌃K k", "Can't be changed", "1-9", "(Sidebar)"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	md := mustRun(t, "keys", "--markdown", "--client", "mac-1")
	for _, want := range []string{"# Agent Command keyboard shortcuts", "## Lists", "| ⇧⌘K | Inbox (changed) |",
		"| (none) | Rename — In Scheduled: edits the job. |", "## Can't be changed"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if !f.calledWith(ipc.OpKeys, `"client_id":"mac-1"`) {
		t.Errorf("--client: %v", f.called())
	}
	var got ipc.Keymap
	must(t, json.Unmarshal([]byte(mustRun(t, "keys", "--json")), &got))
	if got.ClientID != "mac-1" || len(got.Map) == 0 {
		t.Errorf("json: %+v", got)
	}
	if _, err := acmd(t, "keys", "--format", "yaml"); ExitCode(err) != exitUsage {
		t.Errorf("--format is gone: %v", err)
	}
	f.reply(ipc.OpKeys, ipc.Keymap{ClientID: "x", Map: json.RawMessage(`[1]`)})
	if _, err := acmd(t, "keys"); err == nil {
		t.Error("a keymap of another shape read")
	}
}

func TestClientsCommand(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpClients, []ipc.Client{})
	if out := mustRun(t, "clients"); !strings.Contains(out, "No clients yet") {
		t.Errorf("none: %s", out)
	}
	f.reply(ipc.OpClients, []ipc.Client{{ClientHello: ipc.ClientHello{ID: "mac-1", Kind: ipc.KindMac, Device: "studio",
		Caps: []string{ipc.CapNotify}}, Presence: ipc.PresenceActive, Connected: true}})
	out := mustRun(t, "clients")
	for _, want := range []string{"KIND", "mac", "studio", "active", "notify", "mac-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("clients lacks %q:\n%s", want, out)
		}
	}
	if out := mustRun(t, "clients", "--json"); !strings.Contains(out, `"presence":"active"`) {
		t.Errorf("json: %s", out)
	}
}
