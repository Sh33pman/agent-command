package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// Harnesses: what sessions and jobs run in (core.Harness). They're kept in
// the store, changed only through the harness ops, and listed in the
// snapshot. A terminal isn't one.

// harnessIcon is the clients' icon key for a kind.
func harnessIcon(kind string) string { return kind }

// installed says whether h's program is found here (Config.Installed; nil:
// every one is).
func (d *Daemon) installed(h core.Harness) bool {
	return d.cfg.Installed == nil || d.cfg.Installed(h)
}

// claudeDir is the folder Claude reads for h: its own, or Claude's standard
// folder (Config.ClaudeDir) for "".
func (d *Daemon) claudeDir(h core.Harness) string {
	if h.ConfigDir == "" {
		return d.cfg.ClaudeDir
	}
	return core.ExpandHome(h.ConfigDir, d.home())
}

// harnessList is the harnesses, in list order (none when the store can't
// say).
func (d *Daemon) harnessList(ctx context.Context) []core.Harness {
	hs, err := d.store.Harnesses(ctx)
	if err != nil {
		d.cfg.Log.Printf("harnesses: %v", err)
		return nil
	}
	return hs
}

// harnessOf is the harness with id (nil when there's none).
func (d *Daemon) harnessOf(ctx context.Context, id string) *core.Harness {
	if id == "" {
		return nil
	}
	h, err := d.store.Harness(ctx, id)
	if err != nil {
		return nil
	}
	return h
}

func (d *Daemon) wireHarness(h core.Harness) ipc.Harness {
	w := ipc.Harness{Type: ipc.EntityHarness, ID: h.ID, Name: h.Name, Kind: h.Kind, Title: core.KindTitle(h.Kind),
		Icon: harnessIcon(h.Kind), Order: h.Order, Installed: d.installed(h), Command: h.Command, Args: h.Args,
		ConfigDir: h.ConfigDir}
	if h.Kind == core.HarnessClaude {
		w.Folder = d.claudeDir(h)
	}
	return w
}

// defaultHarness is the harness a plain new session runs in: default_harness
// when it names one that's installed, else the first installed one (nil
// when there's none).
func defaultHarness(hs []core.Harness, chosen string, installed func(core.Harness) bool) *core.Harness {
	for i := range hs {
		if hs[i].ID == chosen && installed(hs[i]) {
			return &hs[i]
		}
	}
	for i := range hs {
		if installed(hs[i]) {
			return &hs[i]
		}
	}
	return nil
}

// harnesses are the harnesses as clients list them, and the default.
func (d *Daemon) harnesses(ctx context.Context) ipc.Harnesses {
	hs := d.harnessList(ctx)
	out := ipc.Harnesses{Harnesses: []ipc.Harness{}, Terminal: true}
	for _, h := range hs {
		out.Harnesses = append(out.Harnesses, d.wireHarness(h))
	}
	if def := defaultHarness(hs, d.config().DefaultHarness, d.installed); def != nil {
		out.Default = def.ID
	}
	return out
}

// newHarness resolves what a new session runs in: the harness id names, a
// terminal for kind "terminal", or else the default harness of kind (the
// first installed one) or, with no kind, the default harness. It returns
// nil for a terminal.
func (d *Daemon) newHarness(ctx context.Context, id, kind string) (*core.Harness, error) {
	if id != "" {
		h := d.harnessOf(ctx, id)
		if h == nil {
			return nil, coded(errcode.NotFound, "there's no harness %q", id)
		}
		if kind != "" && kind != h.Kind {
			return nil, coded(errcode.Invalid, "%s is a %s harness, not %s", h.Name, core.KindTitle(h.Kind), core.KindTitle(kind))
		}
		return h, nil
	}
	if kind == core.HarnessTerminal {
		return nil, nil
	}
	hs := d.harnessList(ctx)
	if kind != "" {
		if !core.IsHarnessKind(kind) {
			return nil, coded(errcode.Invalid, "there's no harness kind %q", kind)
		}
		hs = slices.DeleteFunc(hs, func(h core.Harness) bool { return h.Kind != kind })
	}
	if def := defaultHarness(hs, d.config().DefaultHarness, d.installed); def != nil {
		return def, nil
	}
	if len(hs) > 0 {
		return &hs[0], nil // not installed: starting it says so
	}
	if kind != "" {
		return nil, coded(errcode.NotFound, "there's no %s harness", core.KindTitle(kind))
	}
	return nil, coded(errcode.NotFound, "there's no harness: add one in Settings, or with acmd harness add")
}

// harnessForSession is the harness a session runs in now: its own, or, when
// that was removed, the default harness of its kind.
func (d *Daemon) harnessForSession(ctx context.Context, s *core.Session) (*core.Harness, error) {
	if s.IsTerminal() {
		return nil, nil
	}
	if h := d.harnessOf(ctx, s.HarnessID); h != nil {
		return h, nil
	}
	return d.newHarness(ctx, "", cmpKind(s.HarnessKind))
}

func cmpKind(kind string) string {
	if kind == "" {
		return core.HarnessClaude
	}
	return kind
}

// saveHarness adds or changes a harness (harness.add, harness.update).
func (d *Daemon) saveHarness(ctx context.Context, data json.RawMessage, add bool) (any, error) {
	var r ipc.HarnessSave
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, coded(errcode.BadRequest, "%v", err)
	}
	var h core.Harness
	if add {
		if !core.IsHarnessKind(r.Kind) {
			return nil, coded(errcode.Invalid, "a harness is %s, not %q", strings.Join(core.HarnessKinds, " or "), r.Kind)
		}
		h = core.Harness{Kind: r.Kind}
		if r.ConfigDir == nil && r.Kind == core.HarnessClaude {
			dir := core.DefaultConfigDir(r.Name)
			r.ConfigDir = &dir
		}
	} else {
		old := d.harnessOf(ctx, r.ID)
		if old == nil {
			return nil, coded(errcode.NotFound, "there's no harness %q", r.ID)
		}
		if r.Kind != "" && r.Kind != old.Kind {
			return nil, coded(errcode.Invalid, "a harness's kind can't change: add another one")
		}
		h = *old
		if r.Name == "" {
			r.Name = h.Name
		}
	}
	if err := core.CheckHarnessName(r.Name); err != nil {
		return nil, coded(errcode.Invalid, "%v", err)
	}
	h.Name = strings.TrimSpace(r.Name)
	if r.Command != nil {
		h.Command = strings.TrimSpace(*r.Command)
	}
	if r.Args != nil {
		h.Args = slices.DeleteFunc(slices.Clone(*r.Args), func(a string) bool { return strings.TrimSpace(a) == "" })
	}
	if r.ConfigDir != nil {
		if h.Kind != core.HarnessClaude && *r.ConfigDir != "" {
			return nil, coded(errcode.Invalid, "only a Claude harness has a folder")
		}
		h.ConfigDir = strings.TrimSpace(*r.ConfigDir)
	}
	err := d.store.SaveHarness(ctx, &h)
	if errors.Is(err, store.ErrHarnessTaken) {
		return nil, coded(errcode.Invalid, "another harness is already called %q (names ignore case, and \"_\" is a space)", h.Name)
	}
	if err != nil {
		return nil, err
	}
	d.harnessesChanged(ctx)
	return d.wireHarness(h), nil
}

// removeHarness removes a harness (harness.remove), unless a scheduled job
// uses it. Its sessions keep its id, and its folder stays.
func (d *Daemon) removeHarness(ctx context.Context, data json.RawMessage) (any, error) {
	var r ipc.HarnessRef
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, coded(errcode.BadRequest, "%v", err)
	}
	h := d.harnessOf(ctx, r.ID)
	if h == nil {
		return nil, coded(errcode.NotFound, "there's no harness %q", r.ID)
	}
	jobs, err := d.store.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	var using []string
	for _, j := range jobs {
		if j.HarnessID == h.ID {
			using = append(using, fmt.Sprintf("%q", j.Name))
		}
	}
	if len(using) > 0 {
		return nil, coded(errcode.Conflict, "%s runs scheduled jobs: %s. Move them to another harness, or delete them, first", h.Name, strings.Join(using, ", "))
	}
	if err := d.store.RemoveHarness(ctx, h.ID); err != nil {
		return nil, err
	}
	d.harnessesChanged(ctx)
	return nil, nil
}

// harnessSettings lists a harness's settings for jobs (harness.settings).
func (d *Daemon) harnessSettings(ctx context.Context, data json.RawMessage) (any, error) {
	var r ipc.HarnessRef
	if len(data) > 0 {
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, coded(errcode.BadRequest, "%v", err)
		}
	}
	if r.ID == "" && core.IsHarnessKind(r.Kind) {
		return harness.SettingsFor(r.Kind), nil
	}
	h, err := d.newHarness(ctx, r.ID, "")
	if err != nil {
		return nil, err
	}
	return harness.SettingsFor(h.Kind), nil
}

// harnessesChanged tells Claude's hooks about a change (dispatch tells
// clients).
func (d *Daemon) harnessesChanged(ctx context.Context) {
	if d.cfg.HarnessesChanged != nil {
		d.cfg.HarnessesChanged(ctx, d.harnessList(ctx))
	}
}

// coded is an error with its code.
func coded(code, format string, a ...any) error {
	return &codedError{code: code, err: fmt.Errorf(format, a...)}
}

// moveHarnessSettings moves claude_command, claude_args, agy_command and
// agy_args out of config.toml into the first Claude and Antigravity
// harnesses, and turns a default_harness that names a kind into that
// harness's id: once, the first time this daemon meets them.
func (d *Daemon) moveHarnessSettings(ctx context.Context) {
	if d.cfg.ConfigFile == "" {
		return
	}
	f := d.settings()
	old, ok := config.ReadOldHarnesses(f.Text())
	if !ok {
		return
	}
	hs := d.harnessList(ctx)
	first := func(kind string) *core.Harness {
		for i := range hs {
			if hs[i].Kind == kind {
				return &hs[i]
			}
		}
		return nil
	}
	move := func(kind, command string, args []string) {
		h := first(kind)
		if h == nil {
			return
		}
		if c := strings.TrimSpace(command); c != "" && c != core.KindCommand(kind) {
			h.Command = c
		}
		if len(args) > 0 {
			h.Args = args
		}
		if err := d.store.SaveHarness(ctx, h); err != nil {
			d.cfg.Log.Printf("harnesses: moving %s's settings: %v", h.Name, err)
		}
	}
	move(core.HarnessClaude, old.ClaudeCommand, old.ClaudeArgs)
	move(core.HarnessAntigravity, old.AgyCommand, old.AgyArgs)
	err := f.Edit(func(text string) (string, error) {
		text, err := config.RetireHarnessKeys(text)
		if err != nil {
			return text, err
		}
		if old.DefaultHarness != "" {
			id := ""
			if h := first(old.DefaultHarness); h != nil {
				id = h.ID
			}
			return config.Set(text, "", "default_harness", id)
		}
		return text, nil
	})
	if err != nil {
		d.cfg.Log.Printf("harnesses: taking the old settings out of config.toml: %v", err)
		return
	}
	d.followSettings()
	d.cfg.Log.Printf("harnesses: moved claude_command, claude_args, agy_command and agy_args into the harnesses")
}

type harnessesKey struct{}

// HarnessesOf is the harnesses the daemon hands an Init call, so acmd init
// puts its hooks in each Claude harness's folder; ok is false outside one.
func HarnessesOf(ctx context.Context) ([]core.Harness, bool) {
	hs, ok := ctx.Value(harnessesKey{}).([]core.Harness)
	return hs, ok
}
