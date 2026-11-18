package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

func TestConfigSetsRemote(t *testing.T) {
	dir := isolate(t)
	file := filepath.Join(dir, "config.toml")

	var view configView
	must(t, json.Unmarshal([]byte(mustRun(t, "config", "show", "--json")), &view))
	if view.Settings["remote.host.port"] != float64(7433) || view.Settings["remote.host.listen"] != "localhost" ||
		view.Settings["remote.host.port_fallback"] != true || view.Settings["remote.new_sessions_remote"] != false ||
		view.Settings["remote.host.cert"] != "" || view.Settings["remote.client.key"] != "" {
		t.Fatalf("defaults: %+v", view.Settings)
	}
	for _, c := range [][2]string{
		{"remote.new_sessions_remote", "on"}, {"remote.host.listen", "all"}, {"remote.host.port", "7500"},
		{"remote.host.port_fallback", "off"}, {"remote.host.enabled", "on"},
	} {
		mustRun(t, "config", "set", c[0], c[1])
	}
	cfg, err := config.Load(file)
	must(t, err)
	want := config.RemoteHost{Enabled: true, Listen: "all", Port: 7500}
	if !cfg.Remote.NewSessionsRemote || cfg.Remote.Host != want {
		t.Errorf("saved: %+v", cfg.Remote)
	}
	for _, bad := range [][2]string{{"remote.host.port", "0"}, {"remote.host.port", "70000"}, {"remote.host.port", "x"}, {"remote.host.listen", "lan"}} {
		if _, err := acmd(t, "config", "set", bad[0], bad[1]); err == nil {
			t.Errorf("%s = %q accepted", bad[0], bad[1])
		}
	}
	if _, err := acmd(t, "config", "set", "remote.host.cert", "/x"); err == nil || !strings.Contains(err.Error(), "acmd remote cert host") {
		t.Errorf("cert: %v", err)
	}
}

func TestRemoteCert(t *testing.T) {
	dir := isolate(t)
	file := filepath.Join(dir, "config.toml")
	mine, err := identity.Ensure(filepath.Join(t.TempDir(), "mine"))
	must(t, err)

	if out := mustRun(t, "remote", "cert", "host", mine.CertPath, mine.KeyPath); out != "Hosting uses "+mine.CertPath+".\n" {
		t.Errorf("host: %q", out)
	}
	cfg, err := config.Load(file)
	must(t, err)
	if cfg.Remote.Host.Cert != mine.CertPath || cfg.Remote.Host.Key != mine.KeyPath || cfg.Remote.Client.Cert != "" {
		t.Errorf("saved: %+v", cfg.Remote)
	}
	// identity shows both, now they differ.
	var ids struct {
		Fingerprint string
		Client      *struct{ Fingerprint string }
	}
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "identity", "--json")), &ids))
	if ids.Fingerprint != mine.Fingerprint() || ids.Client == nil || ids.Client.Fingerprint == mine.Fingerprint() {
		t.Errorf("identity: %+v", ids)
	}

	if out := mustRun(t, "remote", "cert", "host", "--default"); out != "Hosting uses acmd's identity.\n" {
		t.Errorf("default: %q", out)
	}
	if cfg, _ = config.Load(file); cfg.Remote.Host.Cert != "" || cfg.Remote.Host.Key != "" {
		t.Errorf("default saved: %+v", cfg.Remote.Host)
	}
	if out := mustRun(t, "remote", "cert", "client", mine.CertPath, mine.KeyPath); !strings.HasPrefix(out, "Connecting to hosts uses ") {
		t.Errorf("client: %q", out)
	}

	// A bad pair isn't saved, and says why.
	other, err := identity.Ensure(filepath.Join(t.TempDir(), "other"))
	must(t, err)
	if _, err := acmd(t, "remote", "cert", "host", mine.CertPath, other.KeyPath); err == nil {
		t.Error("a cert with another's key was accepted")
	}
	if cfg, _ = config.Load(file); cfg.Remote.Host.Cert != "" {
		t.Errorf("a bad pair was saved: %+v", cfg.Remote.Host)
	}
	for _, args := range [][]string{
		{"remote", "cert", "server", "--default"}, {"remote", "cert", "host"}, {"remote", "cert", "host", "a"},
		{"remote", "cert", "host", "a", "b", "--default"},
	} {
		if _, err := acmd(t, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestRemoteRename(t *testing.T) {
	p, _ := remoteEnv(t)
	laptop, err := identity.Ensure(filepath.Join(p.StateDir, "laptop"))
	must(t, err)
	desk, err := identity.Ensure(filepath.Join(p.StateDir, "desk"))
	must(t, err)
	mustRun(t, "remote", "authorize", laptop.Fingerprint(), "--name", "laptop")
	mustRun(t, "remote", "authorize", desk.Fingerprint(), "--name", "desk")

	if out := mustRun(t, "remote", "rename", "laptop", "travel"); out != `Renamed "laptop" to "travel" (`+laptop.Fingerprint()+").\n" {
		t.Errorf("rename: %q", out)
	}
	// The daemon follows it at once.
	var clients []ipc.RemoteClient
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "clients", "--json")), &clients))
	names := []string{}
	for _, c := range clients {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "desk,travel" {
		t.Errorf("clients = %v", names)
	}
	if out := mustRun(t, "remote", "rename", desk.Fingerprint(), "office"); !strings.Contains(out, `to "office"`) {
		t.Errorf("by fingerprint: %q", out)
	}
	for _, args := range [][]string{{"nobody", "x"}, {"travel", "office"}, {"travel", ""}} {
		if _, err := acmd(t, append([]string{"remote", "rename"}, args...)...); err == nil {
			t.Errorf("rename %v accepted", args)
		}
	}
	must(t, os.WriteFile(p.Config(), []byte("x = "), 0o600))
	if _, err := acmd(t, "remote", "rename", "travel", "y"); err == nil {
		t.Error("renamed in a config that doesn't parse")
	}
}

func TestConfigSetReloadsHosting(t *testing.T) {
	_, port := remoteEnv(t)
	mustRun(t, "remote", "hosting", "on")
	mustRun(t, "config", "set", "remote.host.port_fallback", "off")
	var st ipc.RemoteStatus
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "status", "--json")), &st))
	if st.PortFallback || !st.Listening || st.Port != port {
		t.Errorf("status = %+v", st)
	}
	// Regenerating the identity changes what Hosting shows.
	before := st.Fingerprint
	mustRun(t, "remote", "identity", "--regenerate", "--yes")
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "status", "--json")), &st))
	if st.Fingerprint == before || st.Fingerprint == "" {
		t.Errorf("fingerprint after regenerating: %s (was %s)", st.Fingerprint, before)
	}
}
