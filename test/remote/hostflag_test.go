package remote

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// acmd runs the CLI on this machine, and returns its output.
func (m *machine) acmd(args ...string) (string, error) {
	m.t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = m.env
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), errors.New(strings.TrimSpace(string(ee.Stderr)))
	}
	return string(out), err
}

// TestHostFlag: acmd --host runs a command on a Remote host through this
// machine's daemon, and sees the whole host (Direct Connect), not only its
// Remote sessions. What only the host's own machine may do is refused.
func TestHostFlag(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	shared := host.newSession("shared", true)
	private := host.newSession("private", false)
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)

	out, err := laptop.acmd("--host", "studio", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	if !ids[shared.ID] || !ids[private.ID] {
		t.Errorf("list on the host = %s, want both its sessions", out)
	}

	// Changes happen on the host.
	if _, err := laptop.acmd("--host", "studio", "rename", private.ID, "--to", "renamed"); err != nil {
		t.Fatal(err)
	}
	if s := host.session(private.ID); s == nil || s.Name != "renamed" {
		t.Errorf("rename on the host: %+v", s)
	}
	host.typeIn(private.ID, "echo hi-there\r")
	laptop.waitFor("the host's screen", func() bool {
		out, err := laptop.acmd("--host", "studio", "screen", private.ID)
		return err == nil && strings.Contains(out, "hi-there")
	})

	// A newly approved machine may act, not admin; nor what's the host's own.
	var rc json.RawMessage
	err = laptop.try(ipc.OpRemoteCall, ipc.RemoteCall{Host: "studio", Op: ipc.OpInit}, &rc)
	var re *ipc.RemoteError
	if !errors.As(err, &re) || re.Code != "permission_denied" {
		t.Errorf("init on the host: %v", err)
	}
	err = laptop.try(ipc.OpRemoteCall, ipc.RemoteCall{Host: "studio", Op: ipc.OpSpeechSay}, &rc)
	if !errors.As(err, &re) || re.Code != "forbidden" {
		t.Errorf("speech on the host: %v", err)
	}
	if _, err := laptop.acmd("--host", "studio", "open", "1"); err == nil || !strings.Contains(err.Error(), "doesn't take --host") {
		t.Errorf("open: %v", err)
	}
	if _, err := laptop.acmd("--host", "nowhere", "list"); err == nil {
		t.Error("an unknown host listed")
	}
	// Without --host, this machine's list, with the host's tree in it: the
	// host's sessions named by their host.
	laptop.hostSession("studio", private.ID)
	out, err = laptop.acmd("list", "--json")
	var here []ipc.Session
	if err != nil || json.Unmarshal([]byte(out), &here) != nil {
		t.Fatalf("laptop's list: %v %s", err, out)
	}
	for _, s := range here {
		if s.ID == private.ID && s.Host != "studio" {
			t.Errorf("the host's session without its host: %+v", s)
		}
	}
}
