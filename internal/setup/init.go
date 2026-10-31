package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
)

// Input is everything PlanInit and PlanUninstall need, gathered by the
// caller (so planning has no side effects and is testable).
type Input struct {
	Home       string
	ConfigFile string // acmd's config.toml
	EnvFile    string // env.json
	AcmdPath   string // stable path written into hooks and keybinds
	ClaudeDir  string // directory holding Claude's settings.json
	KittyDir   string // Kitty's config directory; "" = don't configure Kitty
	ClaudePath string // resolved claude binary; "" = not found
	Keys       Keys
	Editor     *config.Editor    // the editor choice to save; nil = leave as is
	LoginEnv   LoginEnv          // nil = leave env.json as is
	KittyKeys  map[string]string // Kitty's default shortcuts; nil = unavailable
	Read       func(string) ([]byte, error)
}

// ClaudeSettingsFile is the user settings file hooks go into.
func (in Input) ClaudeSettingsFile() string { return filepath.Join(in.ClaudeDir, "settings.json") }

func (in Input) kittyConf() string  { return filepath.Join(in.KittyDir, "kitty.conf") }
func (in Input) kittyOwned() string { return filepath.Join(in.KittyDir, KittyConfName) }

// PlanInit plans `acmd init` (SPEC §9).
func PlanInit(in Input) (*Plan, error) {
	p := &Plan{}
	if err := planClaude(p, in, true); err != nil {
		return nil, err
	}
	if in.KittyDir != "" {
		if err := planKittyInit(p, in); err != nil {
			return nil, err
		}
	}
	if err := planConfig(p, in); err != nil {
		return nil, err
	}
	if in.LoginEnv != nil {
		old, exists, err := readOptional(in.Read, in.EnvFile)
		if err != nil {
			return nil, err
		}
		b, err := in.LoginEnv.MarshalFile()
		if err != nil {
			return nil, err
		}
		p.Changes = append(p.Changes, Change{
			Path: in.EnvFile, Exists: exists, Old: old, New: b, Mode: 0o600, Secret: true,
			Summary: fmt.Sprintf("login-shell environment for processes acmd starts: %d variables (values not shown)", len(in.LoginEnv)),
		})
	}
	if in.ClaudePath == "" {
		p.Notes = append(p.Notes, "claude wasn't found on your login shell's PATH; install Claude Code, then run `acmd init` again")
	}
	return p, nil
}

// PlanUninstall plans `acmd uninstall` (SPEC §9.6): acmd's hook entries,
// managed block and own Kitty file go; nothing else is touched.
func PlanUninstall(in Input) (*Plan, error) {
	p := &Plan{}
	if err := planClaude(p, in, false); err != nil {
		return nil, err
	}
	if in.KittyDir == "" {
		return p, nil
	}
	old, exists, err := readOptional(in.Read, in.kittyConf())
	if err != nil {
		return nil, err
	}
	if exists {
		text, found := RemoveBlock(string(old))
		c := Change{Path: in.kittyConf(), Exists: true, Old: old, New: []byte(text),
			Summary: "remove acmd's include block (your lines are unchanged)"}
		if !found {
			c.Summary = "no acmd block"
		}
		p.Changes = append(p.Changes, c)
	}
	owned, ownedExists, err := readOptional(in.Read, in.kittyOwned())
	if err != nil {
		return nil, err
	}
	if ownedExists {
		p.Changes = append(p.Changes, Change{Path: in.kittyOwned(), Exists: true, Old: owned, Remove: true,
			Summary: "acmd's Kitty shortcuts and remote-control settings"})
		p.After = append(p.After, "Restart Kitty (quit with cmd+q and reopen) to turn acmd's remote control and shortcuts off.")
	}
	return p, nil
}

func planClaude(p *Plan, in Input, add bool) error {
	path := in.ClaudeSettingsFile()
	old, exists, err := readOptional(in.Read, path)
	if err != nil {
		return err
	}
	if !exists && !add {
		return nil
	}
	var out []byte
	if add {
		out, _, err = AddHooks(old, in.AcmdPath)
	} else {
		out, _, err = RemoveHooks(old)
	}
	if err != nil {
		return err
	}
	c := Change{Path: path, Exists: exists, Old: old, New: out, Mode: 0o644}
	user := countHooks(old, false)
	kept := ""
	switch {
	case user == 1:
		kept = "; your 1 other hook entry is kept"
	case user > 1:
		kept = fmt.Sprintf("; your %d other hook entries are kept", user)
	}
	switch {
	case c.Action() == "unchanged" && add:
		c.Summary = "acmd's hooks are already registered"
	case c.Action() == "unchanged":
		c.Summary = "no acmd hooks registered"
	case add:
		c.Summary = fmt.Sprintf("register acmd's hooks for %d events%s", len(HookEvents), kept)
		c.Detail = []string{"command: " + HookCommand(in.AcmdPath, "<Event>")}
	default:
		c.Summary = fmt.Sprintf("remove acmd's %d hook entries%s", countHooks(old, true), kept)
	}
	if exists && c.Action() != "unchanged" && reformats(old) {
		c.Detail = append(c.Detail, "the file is re-indented in Claude's 2-space style (content otherwise unchanged)")
	}
	p.Changes = append(p.Changes, c)
	return nil
}

// countHooks counts hook entries in settings that are (ours) or aren't acmd's.
func countHooks(settings []byte, ours bool) int {
	root, err := parseObject(settings)
	if err != nil {
		return 0
	}
	raw, ok := root.get("hooks")
	if !ok {
		return 0
	}
	var hooks map[string][]hookGroup
	if json.Unmarshal(raw, &hooks) != nil {
		return 0
	}
	n := 0
	for _, groups := range hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if strings.HasSuffix(h.Command, HookMarker) == ours {
					n++
				}
			}
		}
	}
	return n
}

// reformats reports whether rewriting settings changes its layout.
func reformats(settings []byte) bool {
	root, err := parseObject(settings)
	if err != nil {
		return false
	}
	pretty, err := root.pretty()
	return err == nil && string(pretty) != string(settings)
}

func planKittyInit(p *Plan, in Input) error {
	old, exists, err := readOptional(in.Read, in.kittyConf())
	if err != nil {
		return err
	}
	user := KittyConfig{Maps: map[string]string{}, Settings: map[string]string{}}
	if exists {
		if user, err = ParseKittyConfig(in.kittyConf(), in.Read); err != nil {
			return err
		}
	}
	body := "include " + KittyConfName
	text, _ := EnsureBlock(string(old), body)
	conf := Change{Path: in.kittyConf(), Exists: exists, Old: old, New: []byte(text), Mode: 0o644}
	if exists {
		conf.Summary = "add a managed block that includes " + KittyConfName + " (your lines are unchanged)"
	} else {
		conf.Summary = "create it with just the include for " + KittyConfName
	}
	conf.Detail = []string{BlockBegin, body, BlockEnd}
	p.Changes = append(p.Changes, conf)

	binds := KittyBinds(in.AcmdPath, in.Keys)
	skip := Conflicts(binds, user, in.KittyKeys)
	rendered := RenderKittyConf(binds, skip, user)
	ownedOld, ownedExists, err := readOptional(in.Read, in.kittyOwned())
	if err != nil {
		return err
	}
	owned := Change{Path: in.kittyOwned(), Exists: ownedExists, Old: ownedOld, New: []byte(rendered), Mode: 0o644,
		Summary: fmt.Sprintf("acmd's own file: remote control and %d shortcuts", len(binds)-len(skip))}
	owned.Detail = strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	p.Changes = append(p.Changes, owned)

	_, notes := KittyRemoteSettings(user)
	p.Notes = append(p.Notes, notes...)
	for _, b := range binds {
		if reason, ok := skip[NormalizeKey(b.Key, "")]; ok {
			p.Notes = append(p.Notes, fmt.Sprintf("Kitty: skipped %s (%s): %s", b.Key, b.Action, reason))
		}
	}
	if in.KittyKeys == nil {
		p.Notes = append(p.Notes, "Kitty: couldn't read Kitty's built-in shortcuts, so only your own maps were checked for conflicts")
	}

	switch {
	case conf.Action() != "unchanged" || !ownedExists || remoteLines(ownedOld) != remoteLines([]byte(rendered)):
		p.After = append(p.After, "Restart Kitty (quit with cmd+q and reopen): remote control and the socket only take effect at startup.")
	case owned.Action() != "unchanged":
		p.After = append(p.After, "Reload Kitty's config (ctrl+cmd+,) to pick up the changed shortcuts.")
	}
	return nil
}

// remoteLines extracts the settings that need a Kitty restart.
func remoteLines(conf []byte) string {
	var out []string
	for _, l := range strings.Split(string(conf), "\n") {
		if strings.HasPrefix(l, "allow_remote_control ") || strings.HasPrefix(l, "listen_on ") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func planConfig(p *Plan, in Input) error {
	old, exists, err := readOptional(in.Read, in.ConfigFile)
	if err != nil {
		return err
	}
	text := string(old)
	if !exists {
		text = config.Template
	}
	set := func(section, key string, v any) {
		if err == nil {
			text, err = config.Set(text, section, key, v)
		}
	}
	if in.ClaudePath != "" {
		set("", "claude_command", in.ClaudePath)
	}
	if e := in.Editor; e != nil {
		set("editor", "preset", e.Preset)
		set("editor", "command", e.Command)
		set("editor", "resolved", e.Resolved)
		set("editor", "run_in", e.RunIn)
	}
	if err != nil {
		return err
	}
	c := Change{Path: in.ConfigFile, Exists: exists, Old: old, New: []byte(text), Mode: 0o644}
	if exists {
		c.Summary = "update acmd's settings"
		c.Detail = changedLines(string(old), text)
	} else {
		c.Summary = "acmd's settings, from the commented template"
		c.Detail = changedLines(config.Template, text)
	}
	p.Changes = append(p.Changes, c)
	return nil
}

// changedLines lists the lines of b that aren't in a.
func changedLines(a, b string) []string {
	have := map[string]bool{}
	for _, l := range strings.Split(a, "\n") {
		have[l] = true
	}
	var out []string
	for _, l := range strings.Split(b, "\n") {
		if !have[l] && strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// Record is setup.json: proof that init ran, and where it wrote.
type Record struct {
	Version    string    `json:"version"`
	At         time.Time `json:"at"`
	AcmdPath   string    `json:"acmd_path"`
	ClaudeDir  string    `json:"claude_dir"`
	KittyDir   string    `json:"kitty_dir,omitempty"`
	ConfigFile string    `json:"config_file"`
}

// ReadRecord reads setup.json; ok is false if init hasn't run.
func ReadRecord(path string) (Record, bool, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- acmd's own state file
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return r, true, nil
}

// WriteRecord writes setup.json.
func WriteRecord(path string, r Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'), 0o600, true)
}

// ClaudeConfigDir is where Claude Code keeps settings.json: CLAUDE_CONFIG_DIR
// if set, else ~/.claude.
func ClaudeConfigDir(home string, getenv func(string) string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

// KittyConfigDir is Kitty's config directory: KITTY_CONFIG_DIRECTORY, then
// $XDG_CONFIG_HOME/kitty, then ~/.config/kitty.
func KittyConfigDir(home string, getenv func(string) string) string {
	if d := getenv("KITTY_CONFIG_DIRECTORY"); d != "" {
		return d
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "kitty")
	}
	return filepath.Join(home, ".config", "kitty")
}

// StableBinary is the acmd path to write into hooks and keybinds (SPEC
// §4.4): the path acmd was invoked as (argv0, looked up on PATH if it's a
// bare name), never Homebrew's versioned Cellar path, which `brew upgrade`
// removes. exe is os.Executable's result, the fallback.
func StableBinary(argv0, exe string, lookPath func(string) (string, error)) string {
	p := argv0
	switch {
	case p == "":
		p = exe
	case !strings.Contains(p, "/"):
		if found, err := lookPath(p); err == nil {
			p = found
		} else {
			p = exe
		}
	case !filepath.IsAbs(p):
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	if i := strings.Index(p, "/Cellar/"); i >= 0 {
		// <prefix>/Cellar/agent-command/<version>/bin/<name> → <prefix>/bin/<name>
		if link := filepath.Join(p[:i], "bin", filepath.Base(p)); isExecutable(link) {
			return link
		}
	}
	return filepath.Clean(p)
}

// SaveEditor writes the editor choice into config.toml (acmd's own file,
// created from the template if needed), keeping everything else.
func SaveEditor(configFile string, e config.Editor) error {
	p := &Plan{}
	if err := planConfig(p, Input{ConfigFile: configFile, Editor: &e, Read: os.ReadFile}); err != nil {
		return err
	}
	c := p.Changes[0]
	if c.Action() == "unchanged" {
		return nil
	}
	return writeAtomic(configFile, c.New, c.Mode, false)
}
