package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// Settings: the daemon is config.toml's only writer (config.File). Clients
// read the settings from the snapshot and config.get, and change them with
// config.set; hand edits apply as soon as the file changes.

// settings is config.toml, opened on first use.
func (d *Daemon) settings() *config.File {
	d.settingsOnce.Do(func() {
		d.settingsFile = config.OpenFile(d.cfg.ConfigFile)
		d.logProblem()
	})
	return d.settingsFile
}

// config is the settings in effect, after a hand edit too.
func (d *Daemon) config() config.Config {
	f := d.settings()
	if f.ReloadIfChanged() {
		d.logProblem()
	}
	return f.Config()
}

func (d *Daemon) logProblem() {
	if p := d.settingsFile.Problem(); p != nil && d.cfg.Log != nil {
		d.cfg.Log.Printf("%v (the last good settings stay in effect)", p)
	}
}

// startSettings follows config.toml: Hosting and the hosts take a change as
// soon as it's made, by config.set or by hand. (Notifications read it at each
// change of status.) A change seen while reading the settings may be under
// d.mu, so it's followed in its own goroutine.
func (d *Daemon) startSettings(ctx context.Context) {
	f := d.settings()
	f.OnChange(func(config.Config) { go d.followSettings() })
	if d.cfg.ConfigFile != "" {
		go f.Watch(ctx, settingsEvery, d.changes.bump)
	}
}

// followSettings applies the settings to Hosting and the hosts. d.mu isn't
// held.
func (d *Daemon) followSettings() {
	if d.remote != nil {
		d.remote.Reload()
	}
	if d.client != nil {
		_ = d.client.Reload()
	}
}

// settingsEvery is how often config.toml is checked for hand edits.
var settingsEvery = time.Second

// configState is config.get's answer.
func (d *Daemon) configState() ipc.ConfigState {
	d.config()
	return ipc.ConfigOf(d.settings())
}

// setConfig is config.set: the changes, all or none. Choosing an editor
// finds its launcher, as acmd editor set does.
func (d *Daemon) setConfig(set ipc.ConfigSet) (ipc.ConfigState, error) {
	values, err := d.withEditor(set.Values)
	if err != nil {
		return d.configState(), err
	}
	_, err = d.settings().SetIf(values, set.Versions, config.AllowReadOnly)
	if errors.Is(err, config.ErrChanged) {
		err = &codedError{code: ipc.CodeConflict, err: err}
	}
	if err == nil {
		d.followSettings() // now, so the reply shows it in effect
		d.changes.bump()
	}
	return d.configState(), err
}

// withEditor adds editor.resolved to changes that choose an editor: the
// launcher Config.ResolveEditor finds. It refuses one that isn't installed.
// editor.resolved itself isn't set by hand.
func (d *Daemon) withEditor(values map[string]any) (map[string]any, error) {
	if _, ok := values["editor.resolved"]; ok {
		return nil, errors.New("editor.resolved is set by acmd, not by hand")
	}
	_, preset := values["editor.preset"]
	_, command := values["editor.command"]
	if (!preset && !command) || d.cfg.ResolveEditor == nil {
		return values, nil
	}
	e := d.config().Editor
	for key, dst := range map[string]any{"editor.preset": &e.Preset, "editor.command": &e.Command} {
		v, ok := values[key]
		if !ok {
			continue
		}
		spec, _ := config.Find(key)
		checked, err := spec.Check(v)
		if err != nil {
			return nil, err
		}
		switch p := dst.(type) {
		case *string:
			*p = checked.(string)
		case *[]string:
			*p = checked.([]string)
		}
	}
	resolved, err := d.cfg.ResolveEditor(e)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(values)
	out["editor.command"], out["editor.resolved"] = resolved.Command, resolved.Resolved
	if resolved.Command == nil {
		out["editor.command"] = []string{}
	}
	return out, nil
}

// configHealth is what's wrong with config.toml, for the health list.
func (d *Daemon) configHealth() []ipc.Health {
	var out []ipc.Health
	if p := d.settings().Problem(); p != nil {
		step := "Fix config.toml (" + d.settings().Path() + ")."
		if p.Line > 0 {
			step = fmt.Sprintf("Fix line %d of config.toml (%s).", p.Line, d.settings().Path())
		}
		out = append(out, ipc.Health{ID: ipc.HealthConfigInvalid, Severity: ipc.HealthWarning,
			Title:  "config.toml has a mistake",
			Detail: p.Error() + ". The last good settings stay in effect, and settings can't be changed until it's fixed.",
			Fix:    &ipc.HealthFix{Step: step}})
	}
	return out
}

func (d *Daemon) handleConfig(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpConfigGet:
		return d.configState(), nil
	case ipc.OpConfigSet:
		set, err := decode[ipc.ConfigSet](req.Data)
		if err != nil {
			return nil, err
		}
		return d.setConfig(set)
	case ipc.OpRemoteConfigGet:
		ref, err := decode[ipc.RemoteHostRef](req.Data)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, remoteCallTimeout)
		defer cancel()
		return d.client.Config(ctx, ref.Host)
	case ipc.OpRemoteConfigSet:
		set, err := decode[ipc.RemoteConfigSet](req.Data)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, remoteCallTimeout)
		defer cancel()
		return d.client.SetConfig(ctx, set.Host, ipc.ConfigSet{Values: set.Values, Versions: set.Versions})
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

func isConfigOp(op string) bool {
	switch op {
	case ipc.OpConfigGet, ipc.OpConfigSet, ipc.OpRemoteConfigGet, ipc.OpRemoteConfigSet:
		return true
	}
	return false
}

// Remote's data: kept in state.db, not config.toml.

// hostData is Hosting's data in state.db.
type hostData struct{ d *Daemon }

const hostingStateKey = "remote.hosting"

func (h hostData) AuthorizedClients() ([]config.AuthorizedClient, error) {
	return h.d.store.AuthorizedClients(context.Background())
}

func (h hostData) AddAuthorizedClient(c config.AuthorizedClient) error {
	return h.d.store.AddAuthorizedClient(context.Background(), c)
}

func (h hostData) RemoveAuthorizedClient(fp string) (bool, error) {
	return h.d.store.RemoveAuthorizedClient(context.Background(), fp)
}

func (h hostData) HostingState() ([]byte, error) {
	v, err := h.d.store.AppState(context.Background(), hostingStateKey)
	return []byte(v), err
}

func (h hostData) SaveHostingState(b []byte) error {
	return h.d.store.SetAppState(context.Background(), hostingStateKey, string(b))
}

// knownHostData is the known hosts in state.db.
type knownHostData struct{ d *Daemon }

func (k knownHostData) KnownHosts() ([]config.KnownHost, error) {
	return k.d.store.KnownHosts(context.Background())
}

func (k knownHostData) SaveKnownHost(replace string, h config.KnownHost) error {
	return k.d.store.SaveKnownHost(context.Background(), replace, h)
}

func (k knownHostData) RemoveKnownHost(name string) (bool, error) {
	return k.d.store.RemoveKnownHost(context.Background(), name)
}

// movedKey records that config.toml's Remote data was moved to state.db.
const movedKey = "config.remote_moved"

// BackupSuffix names config.toml's copy from before its Remote data moved.
const BackupSuffix = "before-state-db"

// moveRemoteData moves, once, what ops manage out of config.toml and
// remote-host.json into state.db: the authorized clients, the known hosts,
// the pending requests and the fallback port. config.toml is backed up
// first. Entries written into config.toml afterwards still count, read-only.
func (d *Daemon) moveRemoteData(ctx context.Context) error {
	if done, err := d.store.AppState(ctx, movedKey); err != nil || done != "" {
		return err
	}
	f := d.settings()
	if p := f.Problem(); p != nil {
		return fmt.Errorf("not moving Remote's data out of config.toml yet: %w", p)
	}
	r := f.Config().Remote
	if len(r.AuthorizedClients) > 0 || len(r.Hosts) > 0 {
		backup, err := f.Backup(BackupSuffix)
		if err != nil {
			return fmt.Errorf("backing up config.toml: %w", err)
		}
		for _, c := range r.AuthorizedClients {
			if fp, err := identity.ParseFingerprint(c.Fingerprint); err == nil {
				c.Fingerprint = fp
			}
			if err := d.store.AddAuthorizedClient(ctx, c); err != nil {
				return err
			}
		}
		have, err := d.store.KnownHosts(ctx)
		if err != nil {
			return err
		}
		for _, h := range r.Hosts {
			if strings.TrimSpace(h.Name) == "" || containsHost(have, h.Name) {
				continue
			}
			if err := d.store.SaveKnownHost(ctx, "", h); err != nil {
				return err
			}
			have = append(have, h)
		}
		if err := f.Edit(func(text string) (string, error) {
			text, _, err := config.RemoveTables(text, "remote.authorized_clients", func(map[string]any) bool { return true })
			if err != nil {
				return "", err
			}
			text, _, err = config.RemoveTables(text, "remote.hosts", func(map[string]any) bool { return true })
			return text, err
		}); err != nil {
			return err
		}
		d.cfg.Log.Printf("config: moved %d authorized clients and %d Remote hosts from config.toml to state.db (the file as it was is %s)",
			len(r.AuthorizedClients), len(r.Hosts), backup)
	}
	if path := d.cfg.Paths.RemoteHostState(); path != "" {
		b, err := os.ReadFile(path) // #nosec G304 -- acmd's own state file
		switch {
		case err == nil:
			if json.Valid(b) {
				if err := d.store.SetAppState(ctx, hostingStateKey, string(b)); err != nil {
					return err
				}
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			d.cfg.Log.Printf("config: moved %s to state.db", path)
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
	}
	return d.store.SetAppState(ctx, movedKey, d.cfg.Now().UTC().Format(time.RFC3339))
}

func containsHost(list []config.KnownHost, name string) bool {
	for _, h := range list {
		if h.Name == name {
			return true
		}
	}
	return false
}
