package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// settingsPaths are test paths with a config folder.
func settingsPaths(t *testing.T) paths.Paths {
	t.Helper()
	p := testPaths(t)
	p.ConfigDir = filepath.Join(filepath.Dir(p.StateDir), "c")
	return p
}

func withConfig(p paths.Paths, more ...func(*Config)) func(*Config) {
	return func(c *Config) {
		c.ConfigFile = p.Config()
		for _, fn := range more {
			fn(c)
		}
	}
}

func snapshotOf(t *testing.T, c *ipc.Conn) ipc.Snapshot {
	t.Helper()
	var s ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &s))
	return s
}

func TestConfigOps(t *testing.T) {
	p := settingsPaths(t)
	must(t, writeSetting(p.Config(), "notify", "cooldown", "20s"))
	start(t, p, withConfig(p))
	c := dial(t, p)

	var st ipc.ConfigState
	must(t, c.Call(ipc.OpConfigGet, nil, &st))
	if st.Path != p.Config() || len(st.Settings) != len(config.Specs) || st.Values["notify.cooldown"] != "20s" || st.Problem != nil {
		t.Fatalf("config = %+v", st)
	}
	// Many at once, as the app sends them and as they're typed.
	must(t, c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{
		"notify.cooldown": "1m", "agent_remote_control": "on", "claude_args": []string{"--model", "opus"}}}, &st))
	if st.Values["notify.cooldown"] != "1m0s" || st.Values["agent_remote_control"] != true {
		t.Errorf("after set = %+v", st.Values)
	}
	cfg, err := config.Load(p.Config())
	must(t, err)
	if cfg.Notify.Cooldown != "1m0s" || !cfg.AgentRemoteControl || strings.Join(cfg.ClaudeArgs, " ") != "--model opus" {
		t.Errorf("config.toml = %+v", cfg)
	}
	// The snapshot has the settings in effect.
	if s := snapshotOf(t, c); s.Settings["agent_remote_control"] != true || len(s.Health) != 0 {
		t.Errorf("snapshot settings = %v, health %+v", s.Settings["agent_remote_control"], s.Health)
	}
	// All or none.
	err = c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"notify.sound": "", "remote.host.port": "nope"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "remote.host.port") {
		t.Errorf("bad value: %v", err)
	}
	if cfg, _ := config.Load(p.Config()); cfg.Notify.Sound != "default" {
		t.Error("half a refused change was made")
	}
	// acmd sets the editor's launcher, not a client.
	if err := c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"editor.resolved": "/x"}}, nil); err == nil {
		t.Error("set editor.resolved")
	}
	if err := c.Call(ipc.OpConfigSet, "nope", nil); err == nil {
		t.Error("took a change that isn't one")
	}
}

// Two changes at once, from two clients, both land.
func TestConfigSetsAtOnce(t *testing.T) {
	p := settingsPaths(t)
	start(t, p, withConfig(p))
	keys := []string{"notify.on_waiting", "notify.on_error", "notify.on_done", "remote.new_sessions_remote"}
	var wg sync.WaitGroup
	for _, k := range keys {
		c := dial(t, p)
		wg.Go(func() {
			if err := c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{k: k == "remote.new_sessions_remote"}}, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	cfg, err := config.Load(p.Config())
	must(t, err)
	if cfg.Notify.OnWaiting || cfg.Notify.OnError || cfg.Notify.OnDone || !cfg.Remote.NewSessionsRemote {
		t.Fatalf("a change was lost: %+v %+v", cfg.Notify, cfg.Remote)
	}
}

// A hand edit applies by itself; a bad one keeps the last good settings,
// shows in the snapshot's health, and refuses changes until it's fixed.
func TestHandEditsApplyLive(t *testing.T) {
	p := settingsPaths(t)
	must(t, writeSetting(p.Config(), "", "default_harness", "claude"))
	old := settingsEvery
	settingsEvery = 10 * time.Millisecond
	t.Cleanup(func() { settingsEvery = old })
	start(t, p, withConfig(p))
	c := dial(t, p)

	waitFor := func(what string, ok func(ipc.Snapshot) bool) ipc.Snapshot {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; {
			s := snapshotOf(t, c)
			if ok(s) {
				return s
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v %+v", what, s.Settings["default_harness"], s.Health)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	bump := func(n int) {
		at := time.Now().Add(time.Duration(n) * time.Second)
		must(t, os.Chtimes(p.Config(), at, at))
	}
	must(t, writeSetting(p.Config(), "", "default_harness", "antigravity"))
	bump(1)
	waitFor("a hand edit", func(s ipc.Snapshot) bool { return s.Settings["default_harness"] == "antigravity" })

	b, _ := os.ReadFile(p.Config())
	// Written whole, as an editor saves: a reload never reads it half written.
	tmp := p.Config() + ".tmp"
	must(t, os.WriteFile(tmp, append(b, []byte("\nbroken = \n")...), 0o600))
	must(t, os.Rename(tmp, p.Config()))
	bump(2)
	s := waitFor("a bad edit", func(s ipc.Snapshot) bool { return len(s.Health) == 1 })
	h := s.Health[0]
	if h.ID != ipc.HealthConfigInvalid || h.Severity != ipc.HealthWarning || h.Fix == nil || !strings.Contains(h.Fix.Step, "Fix line") ||
		!strings.Contains(h.Detail, "last good settings stay in effect") {
		t.Errorf("health = %+v", h)
	}
	if s.Settings["default_harness"] != "antigravity" {
		t.Error("the last good settings weren't kept")
	}
	err := c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"notify.sound": ""}}, nil)
	if err == nil || !strings.Contains(err.Error(), "fix it first") {
		t.Errorf("set on a bad file: %v", err)
	}
	var st ipc.ConfigState
	must(t, c.Call(ipc.OpConfigGet, nil, &st))
	if st.Problem == nil || st.Problem.Line == 0 {
		t.Errorf("problem = %+v", st.Problem)
	}

	must(t, os.WriteFile(p.Config(), b, 0o600))
	bump(3)
	waitFor("the fix", func(s ipc.Snapshot) bool { return len(s.Health) == 0 })
}

// On upgrade, the authorized clients and known hosts move from config.toml
// to state.db once, after it's backed up, and keep working.
func TestRemoteDataMovesToStateDB(t *testing.T) {
	p := settingsPaths(t)
	laptop, err := identity.Ensure(filepath.Join(filepath.Dir(p.StateDir), "laptop"))
	must(t, err)
	before := "# mine\nagent_remote_control = true\n\n" +
		"[[remote.authorized_clients]]\nname = \"laptop\"\nfingerprint = \"" + laptop.Fingerprint() + "\"\n\n" +
		"[[remote.hosts]]\nname = \"studio\"\naddress = \"127.0.0.1:1\"\nfingerprint = \"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"\n"
	must(t, os.MkdirAll(p.ConfigDir, 0o700))
	must(t, os.WriteFile(p.Config(), []byte(before), 0o600))
	must(t, os.MkdirAll(p.StateDir, 0o700))
	must(t, os.WriteFile(p.RemoteHostState(), []byte(`{"port":{"listen":"localhost","configured":7433,"port":7500}}`), 0o600))

	r := start(t, p, withConfig(p))
	c := dial(t, p)
	var clients []ipc.RemoteClient
	must(t, c.Call(ipc.OpRemoteClients, nil, &clients))
	if len(clients) != 1 || clients[0].Name != "laptop" || clients[0].InConfig {
		t.Errorf("clients = %+v", clients)
	}
	var hosts []ipc.RemoteHost
	must(t, c.Call(ipc.OpRemoteHosts, nil, &hosts))
	if len(hosts) != 1 || hosts[0].Name != "studio" || hosts[0].InConfig {
		t.Errorf("hosts = %+v", hosts)
	}
	// config.toml keeps everything else, and its copy from before is kept.
	after, _ := os.ReadFile(p.Config())
	if strings.Contains(string(after), "[[remote.") || !strings.Contains(string(after), "# mine\nagent_remote_control = true") {
		t.Errorf("config.toml:\n%s", after)
	}
	if b, err := os.ReadFile(p.Config() + "." + BackupSuffix); err != nil || string(b) != before {
		t.Errorf("backup: %q %v", b, err)
	}
	if _, err := os.Stat(p.RemoteHostState()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remote-host.json is still there: %v", err)
	}
	// They're managed now: the client can be revoked.
	var rc ipc.RemoteClient
	must(t, c.Call(ipc.OpRemoteRevoke, ipc.RemoteRef{Client: "laptop"}, &rc))
	r.cancel()
	<-r.Done

	// Once only: an entry written into the file later stays there, read-only.
	again := string(after) + "\n[[remote.authorized_clients]]\nname = \"by-hand\"\nfingerprint = \"" + laptop.Fingerprint() + "\"\n"
	must(t, os.WriteFile(p.Config(), []byte(again), 0o600))
	start(t, p, withConfig(p))
	c = dial(t, p)
	must(t, c.Call(ipc.OpRemoteClients, nil, &clients))
	if len(clients) != 1 || clients[0].Name != "by-hand" || !clients[0].InConfig {
		t.Errorf("clients = %+v", clients)
	}
	if err := c.Call(ipc.OpRemoteRevoke, ipc.RemoteRef{Client: "by-hand"}, &rc); err == nil || !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("revoke one in config.toml: %v", err)
	}
	if b, _ := os.ReadFile(p.Config()); string(b) != again {
		t.Errorf("config.toml changed:\n%s", b)
	}
}

func TestChoosingAnEditorFindsIt(t *testing.T) {
	p := settingsPaths(t)
	var asked []config.Editor
	start(t, p, withConfig(p, func(c *Config) {
		c.ResolveEditor = func(e config.Editor) (config.Editor, error) {
			asked = append(asked, e)
			switch e.Preset {
			case "zed":
				e.Resolved = "/usr/local/bin/zed"
			case "custom":
				e.Resolved = "/bin/" + e.Command[0]
				e.Command = append(e.Command, "{dir}")
			default:
				return e, errors.New(e.Preset + " isn't installed")
			}
			return e, nil
		}
	}))
	c := dial(t, p)
	var st ipc.ConfigState
	must(t, c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"editor.preset": "zed", "editor.target": "git_root"}}, &st))
	if st.Values["editor.resolved"] != "/usr/local/bin/zed" || st.Values["editor.target"] != "git_root" {
		t.Errorf("zed = %+v", st.Values)
	}
	must(t, c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"editor.preset": "custom", "editor.command": "nvim"}}, &st))
	if list, ok := st.Values["editor.command"].([]any); !ok || len(list) != 2 || list[1] != "{dir}" || st.Values["editor.resolved"] != "/bin/nvim" {
		t.Errorf("custom = %+v", st.Values)
	}
	if err := c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"editor.preset": "idea"}}, nil); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Errorf("not installed: %v", err)
	}
	if err := c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"editor.preset": "notepad"}}, nil); err == nil {
		t.Error("an unknown editor")
	}
	if len(asked) != 3 {
		t.Errorf("asked %d times", len(asked))
	}
}

// This Mac's identity, its own certs, a new identity, and authorizing a
// client by its cert file: all through the daemon.
func TestIdentityOps(t *testing.T) {
	p := settingsPaths(t)
	start(t, p, withConfig(p))
	c := dial(t, p)

	var id ipc.RemoteIdentity
	must(t, c.Call(ipc.OpRemoteIdentity, nil, &id))
	if id.Fingerprint == "" || id.Client != nil || strings.Join(id.UsedFor, ",") != "hosting,client" || id.Address != "" {
		t.Fatalf("identity = %+v", id)
	}
	first := id.Fingerprint

	// A cert of your own for connecting to hosts: two identities now.
	own, err := identity.Ensure(filepath.Join(filepath.Dir(p.StateDir), "own"))
	must(t, err)
	must(t, c.Call(ipc.OpRemoteCert, ipc.RemoteCert{Role: "client", Cert: own.CertPath, Key: own.KeyPath}, &id))
	if id.Client == nil || id.Client.Fingerprint != own.Fingerprint() || id.Fingerprint != first {
		t.Errorf("own client cert = %+v", id)
	}
	if cfg, _ := config.Load(p.Config()); cfg.Remote.Client.Cert != own.CertPath || cfg.Remote.Client.Key != own.KeyPath {
		t.Errorf("config.toml = %+v", cfg.Remote.Client)
	}
	other, err := identity.Ensure(filepath.Join(filepath.Dir(p.StateDir), "other"))
	must(t, err)
	for _, bad := range []ipc.RemoteCert{
		{Role: "host", Cert: own.CertPath, Key: other.KeyPath}, // not a pair
		{Role: "server", Default: true},
		{Role: "host", Cert: own.CertPath},
		{Role: "host", Cert: "cert.pem", Key: "key.pem"},
	} {
		if err := c.Call(ipc.OpRemoteCert, bad, nil); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	id = ipc.RemoteIdentity{}
	must(t, c.Call(ipc.OpRemoteCert, ipc.RemoteCert{Role: "client", Default: true}, &id))
	if id.Client != nil {
		t.Errorf("back to acmd's = %+v", id)
	}

	// A new identity.
	must(t, c.Call(ipc.OpRemoteRegenerate, nil, &id))
	if id.Fingerprint == first || id.Fingerprint == "" {
		t.Errorf("regenerated = %+v", id)
	}

	// Authorizing by cert file names the client after its cert.
	var rc ipc.RemoteClient
	must(t, c.Call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Cert: own.CertPath}, &rc))
	if rc.Fingerprint != own.Fingerprint() || rc.Name != own.CommonName {
		t.Errorf("authorized = %+v", rc)
	}
	if err := c.Call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Cert: "/nowhere.pem"}, nil); err == nil {
		t.Error("authorized a missing cert")
	}
	if err := c.Call(ipc.OpRemoteCert, "nope", nil); err == nil {
		t.Error("took a cert that isn't one")
	}

	// A broken cert in the settings is said.
	must(t, writeSetting(p.Config(), "remote.host", "cert", "/nowhere.pem"))
	must(t, writeSetting(p.Config(), "remote.host", "key", "/nowhere-key.pem"))
	later := time.Now().Add(time.Minute)
	must(t, os.Chtimes(p.Config(), later, later))
	if err := c.Call(ipc.OpRemoteIdentity, nil, &id); err == nil || !strings.Contains(err.Error(), "Hosting identity") {
		t.Errorf("broken host cert: %v", err)
	}
}

func TestHarnessesOp(t *testing.T) {
	p := settingsPaths(t)
	must(t, writeSetting(p.Config(), "", "default_harness", "antigravity"))
	start(t, p, withConfig(p, func(c *Config) {
		c.Harnesses = func() []string { return []string{"claude", "antigravity", "terminal", "copilot"} }
	}))
	c := dial(t, p)
	var h ipc.Harnesses
	must(t, c.Call(ipc.OpHarnesses, nil, &h))
	var names []string
	for _, e := range h.Harnesses {
		names = append(names, e.Name+":"+e.Icon)
	}
	if strings.Join(names, ",") != "claude:claude,antigravity:antigravity,terminal:terminal" || h.Default != "antigravity" || !h.Harnesses[0].Agent || h.Harnesses[2].Agent {
		t.Errorf("harnesses = %+v", h)
	}
}
