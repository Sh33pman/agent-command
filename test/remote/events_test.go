package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// TestEvents: an agent in a session on the host asks for permission, through
// acmd's real hook, and the client sees the session wait and the question in
// its inbox.
func TestEvents(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	s := host.newSession("agent", true)
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)
	local := laptop.hostSession("studio", s.ID)

	// The agent: it sends its hooks as Claude Code does, and stays in front of
	// the terminal, as an agent waiting for an answer does.
	payload := filepath.Join(host.home, "perm.json")
	write(t, payload, `{"session_id":"c-1","hook_event_name":"PermissionRequest","tool_name":"Bash",`+
		`"tool_input":{"command":"make deploy"},"cwd":"`+s.Cwd+`"}`)
	agent := filepath.Join(host.home, "agent.sh")
	write(t, agent, fmt.Sprintf("%q hook PermissionRequest < %q\nexec sleep 600\n", bin, payload))
	host.typeIn(s.ID, "sh "+agent+"\n")

	var item core.InboxItem
	laptop.waitFor("the inbox item", func() bool {
		for _, it := range laptop.snapshot().Inbox {
			if it.SessionID == local.ID {
				item = it
				return true
			}
		}
		return false
	})
	if item.Host != "studio" || item.Kind != core.InboxPermission || !strings.Contains(item.Text, "make deploy") || item.ID >= 0 {
		t.Errorf("item = %+v", item)
	}
	laptop.waitFor("the session to wait", func() bool {
		got := laptop.session(local.ID)
		return got != nil && got.Status == core.StatusWaiting
	})
	if on := host.session(s.ID); on.Status != core.StatusWaiting {
		t.Errorf("on the host: %s", on.Status)
	}
	var items []core.InboxItem
	laptop.call(ipc.OpInbox, ipc.InboxRequest{SessionID: local.ID}, &items)
	if len(items) != 1 || items[0].ID != item.ID {
		t.Errorf("the session's inbox = %+v", items)
	}
}

func write(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
