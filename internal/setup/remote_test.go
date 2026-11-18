package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
)

func TestAuthorizedClients(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	laptop := config.AuthorizedClient{Name: "laptop", Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	desk := config.AuthorizedClient{Name: "desk", Fingerprint: "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"}

	// Nothing to remove from a file that isn't there, and nothing is written.
	if n, err := RemoveAuthorizedClients(file, func(config.AuthorizedClient) bool { return true }); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("wrote the file: %v", err)
	}

	// A missing file starts from the template; a comment of the user's stays.
	must(t, AddAuthorizedClient(file, laptop))
	b, _ := os.ReadFile(file)
	must(t, os.WriteFile(file, append([]byte("# mine\n"), b...), 0o600))
	must(t, AddAuthorizedClient(file, desk))
	must(t, SaveSetting(file, "remote.host", "enabled", true))
	c, err := config.Load(file)
	must(t, err)
	if len(c.Remote.AuthorizedClients) != 2 || c.Remote.AuthorizedClients[0] != laptop || !c.Remote.Host.Enabled {
		t.Fatalf("remote = %+v", c.Remote)
	}

	n, err := RemoveAuthorizedClients(file, func(a config.AuthorizedClient) bool { return a.Name == "laptop" })
	if n != 1 || err != nil {
		t.Fatal(n, err)
	}
	c, err = config.Load(file)
	must(t, err)
	if len(c.Remote.AuthorizedClients) != 1 || c.Remote.AuthorizedClients[0] != desk || !c.Remote.Host.Enabled {
		t.Errorf("remote = %+v", c.Remote)
	}
	b, _ = os.ReadFile(file)
	if !strings.HasPrefix(string(b), "# mine\n# agent-command (acmd) configuration.") || !strings.Contains(string(b), "# [[remote.authorized_clients]]") {
		t.Errorf("comments lost:\n%s", b)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}

	must(t, AddAuthorizedClient(file, laptop))
	ok, err := RenameAuthorizedClient(file, desk.Fingerprint, "office")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	c, err = config.Load(file)
	must(t, err)
	office := config.AuthorizedClient{Name: "office", Fingerprint: desk.Fingerprint}
	if len(c.Remote.AuthorizedClients) != 2 || c.Remote.AuthorizedClients[0] != laptop || c.Remote.AuthorizedClients[1] != office {
		t.Errorf("renamed: %+v", c.Remote.AuthorizedClients)
	}
	if ok, err := RenameAuthorizedClient(file, "SHA256:none", "x"); ok || err != nil {
		t.Errorf("renamed one that isn't there: %v %v", ok, err)
	}

	must(t, os.WriteFile(file, []byte("x = "), 0o600))
	if err := AddAuthorizedClient(file, desk); err == nil {
		t.Error("added to a file that doesn't parse")
	}
	if _, err := RenameAuthorizedClient(file, desk.Fingerprint, "x"); err == nil {
		t.Error("renamed in a file that doesn't parse")
	}
}

func TestSaveSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	must(t, SaveSettings(file, "remote.client", config.KeyValue{Key: "cert", Value: "/c.pem"}, config.KeyValue{Key: "key", Value: "/k.pem"}))
	c, err := config.Load(file)
	must(t, err)
	if c.Remote.Client.Cert != "/c.pem" || c.Remote.Client.Key != "/k.pem" {
		t.Errorf("client = %+v", c.Remote.Client)
	}
	must(t, os.WriteFile(file, []byte("x = "), 0o600))
	if err := SaveSettings(file, "remote.client", config.KeyValue{Key: "cert", Value: ""}); err == nil {
		t.Error("saved to a file that doesn't parse")
	}
}
