package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	ConfigFile string         // acmd's config.toml
	EnvFile    string         // env.json
	AcmdPath   string         // stable path written into hooks and keybinds
	ClaudeDir  string         // directory holding Claude's settings.json
	ClaudePath string         // resolved claude binary; "" = not found
	Editor     *config.Editor // the editor choice to save; nil = leave as is
	LoginEnv   LoginEnv       // nil = leave env.json as is
	Read       func(string) ([]byte, error)

	// AntigravityDir holds agy's hooks.json (AntigravityConfigDir); "" = leave it alone.
	AntigravityDir string
	// AgyPath is the resolved agy binary; "" = not installed, so no hooks for it.
	AgyPath string
}

// ClaudeSettingsFile is the user settings file hooks go into.
func (in Input) ClaudeSettingsFile() string { return filepath.Join(in.ClaudeDir, "settings.json") }

// PlanInit plans `acmd init`.
func PlanInit(in Input) (*Plan, error) {
	p := &Plan{}
	if err := planClaude(p, in, true); err != nil {
		return nil, err
	}
	if err := planAntigravity(p, in, true); err != nil {
		return nil, err
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

// PlanUninstall plans `acmd uninstall`: acmd's hook entries go (Claude's and
// Antigravity's), and nothing else is touched.
func PlanUninstall(in Input) (*Plan, error) {
	p := &Plan{}
	if err := planClaude(p, in, false); err != nil {
		return nil, err
	}
	if err := planAntigravity(p, in, false); err != nil {
		return nil, err
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
		out, err = resetHooks(old, in.AcmdPath)
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

// resetHooks takes out every acmd hook and adds the current set, so nothing stale
// survives: an old binary path, or an event no longer used. Claude is the only
// agent with hooks for now.
func resetHooks(settings []byte, acmdPath string) ([]byte, error) {
	clean, _, err := RemoveHooks(settings)
	if err != nil {
		return nil, err
	}
	out, _, err := AddHooks(clean, acmdPath)
	return out, err
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

func planConfig(p *Plan, in Input) error {
	old, exists, err := readOptional(in.Read, in.ConfigFile)
	if err != nil {
		return err
	}
	text := string(old)
	if !exists {
		text = config.Template
	}
	if text, err = config.Retire(text); err != nil {
		return err
	}
	set := func(section, key string, v any) {
		if err == nil {
			text, err = config.Set(text, section, key, v)
		}
	}
	if in.ClaudePath != "" {
		set("", "claude_command", in.ClaudePath)
	}
	if in.AgyPath != "" {
		set("", "agy_command", in.AgyPath)
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
	// A link to the binary in an app (the cask's acmd) means the app's own path, so
	// the app and the command line agree on what the hooks run.
	if resolved, err := filepath.EvalSymlinks(p); err == nil && strings.Contains(resolved, ".app/Contents/MacOS/") {
		return resolved
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

// SaveSetting sets one value in config.toml, keeping the rest of the file as it
// is, apart from what config.Retire migrates or takes out. A missing file
// starts from the template.
func SaveSetting(configFile, section, key string, value any) error {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return err
	}
	if text, err = config.Set(text, section, key, value); err != nil {
		return err
	}
	return writeConfig(configFile, orig, text)
}

// SaveSettings sets several values in one section of config.toml with one
// write, so a cert and its key never disagree in between.
func SaveSettings(configFile, section string, kv ...config.KeyValue) error {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return err
	}
	for _, p := range kv {
		if text, err = config.Set(text, section, p.Key, p.Value); err != nil {
			return err
		}
	}
	return writeConfig(configFile, orig, text)
}

// RenameAuthorizedClient gives the authorized client with this fingerprint a
// new name, with one write. Its entry moves to the end of the list. It says
// whether there was one.
func RenameAuthorizedClient(configFile, fingerprint, name string) (bool, error) {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return false, err
	}
	text, n, err := config.RemoveTables(text, "remote.authorized_clients", func(t map[string]any) bool {
		fp, _ := t["fingerprint"].(string)
		return fp == fingerprint
	})
	if err != nil || n == 0 {
		return false, err
	}
	text, err = config.AppendTable(text, "remote.authorized_clients",
		config.KeyValue{Key: "name", Value: name}, config.KeyValue{Key: "fingerprint", Value: fingerprint})
	if err != nil {
		return false, err
	}
	return true, writeConfig(configFile, orig, text)
}

// AddAuthorizedClient adds a [[remote.authorized_clients]] entry to the end of
// config.toml, keeping the rest of the file as SaveSetting does.
func AddAuthorizedClient(configFile string, c config.AuthorizedClient) error {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return err
	}
	text, err = config.AppendTable(text, "remote.authorized_clients",
		config.KeyValue{Key: "name", Value: c.Name}, config.KeyValue{Key: "fingerprint", Value: c.Fingerprint})
	if err != nil {
		return err
	}
	return writeConfig(configFile, orig, text)
}

// RemoveAuthorizedClients takes the [[remote.authorized_clients]] entries that
// match out of config.toml, keeping the rest of the file, and says how many it
// took.
func RemoveAuthorizedClients(configFile string, match func(config.AuthorizedClient) bool) (int, error) {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return 0, err
	}
	text, n, err := config.RemoveTables(text, "remote.authorized_clients", func(t map[string]any) bool {
		name, _ := t["name"].(string)
		fp, _ := t["fingerprint"].(string)
		return match(config.AuthorizedClient{Name: name, Fingerprint: fp})
	})
	if err != nil {
		return 0, err
	}
	return n, writeConfig(configFile, orig, text)
}

// SetKnownHost writes a [[remote.hosts]] entry to config.toml, keeping the
// rest of the file: it takes out the entry named replace first (when not ""),
// then adds h at the end, in one write.
func SetKnownHost(configFile, replace string, h config.KnownHost) error {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return err
	}
	if replace != "" {
		if text, _, err = config.RemoveTables(text, "remote.hosts", func(t map[string]any) bool {
			name, _ := t["name"].(string)
			return name == replace
		}); err != nil {
			return err
		}
	}
	kv := []config.KeyValue{{Key: "name", Value: h.Name}, {Key: "address", Value: h.Address}, {Key: "fingerprint", Value: h.Fingerprint}}
	if h.Cert != "" {
		kv = append(kv, config.KeyValue{Key: "cert", Value: h.Cert})
	}
	if text, err = config.AppendTable(text, "remote.hosts", kv...); err != nil {
		return err
	}
	return writeConfig(configFile, orig, text)
}

// RemoveKnownHost takes the [[remote.hosts]] entries named name out of
// config.toml, keeping the rest of the file, and says how many it took.
func RemoveKnownHost(configFile, name string) (int, error) {
	orig, text, err := readConfig(configFile)
	if err != nil {
		return 0, err
	}
	text, n, err := config.RemoveTables(text, "remote.hosts", func(t map[string]any) bool {
		v, _ := t["name"].(string)
		return v == name
	})
	if err != nil {
		return 0, err
	}
	return n, writeConfig(configFile, orig, text)
}

// readConfig reads config.toml as it is, and as config.Retire makes it. A
// missing file is the template.
func readConfig(configFile string) (orig, text string, err error) {
	b, err := os.ReadFile(configFile) // #nosec G304 -- acmd's own config file
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b = []byte(config.Template)
	case err != nil:
		return "", "", err
	}
	text, err = config.Retire(string(b))
	return string(b), text, err
}

// writeConfig writes config.toml when text differs from what was read.
func writeConfig(configFile, orig, text string) error {
	if text == orig {
		return nil
	}
	return writeAtomic(configFile, []byte(text), 0o600, false)
}

// RetireOld takes acmd v1's settings out of config.toml (config.Retire), keeping
// the rest as it is, and says whether it changed the file. The background
// service runs it as it starts, so an upgrade tidies the file without `acmd init`.
func RetireOld(configFile string) (bool, error) {
	b, err := os.ReadFile(configFile) // #nosec G304 -- acmd's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	text, err := config.Retire(string(b))
	if err != nil || text == string(b) {
		return false, err
	}
	return true, writeAtomic(configFile, []byte(text), 0o600, false)
}
