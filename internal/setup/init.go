package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
)

// Input is everything PlanInit and PlanUninstall need, gathered by the
// caller (so planning has no side effects and is testable).
type Input struct {
	// ClientOnly is a client-only daemon's machine (Windows): it runs no
	// sessions, so init sets up acmd's settings and nothing for the agents.
	ClientOnly bool
	Home       string
	ConfigFile string // acmd's config.toml
	EnvFile    string // env.json
	AcmdPath   string // stable path written into hooks
	ClaudeDir  string // Claude's standard folder, holding its settings.json
	// HarnessDirs are the Claude harnesses' own folders (CLAUDE_CONFIG_DIR):
	// acmd's hooks go in each, as in ClaudeDir.
	HarnessDirs []string
	ClaudePath  string   // resolved claude binary; "" = not found
	LoginEnv    LoginEnv // nil = leave env.json as is
	Read        func(string) ([]byte, error)

	// AntigravityDir holds agy's hooks.json (AntigravityConfigDir); "" = leave it alone.
	AntigravityDir string
	// AgyPath is the resolved agy binary; "" = not installed, so no hooks for it.
	AgyPath string
}

// ClaudeSettingsFile is the user settings file hooks go into.
func (in Input) ClaudeSettingsFile() string { return filepath.Join(in.ClaudeDir, "settings.json") }

// ClaudeDirs are every folder acmd's hooks go in: ClaudeDir, then each
// harness's own, once each.
func (in Input) ClaudeDirs() []string {
	var out []string
	for _, d := range append([]string{in.ClaudeDir}, in.HarnessDirs...) {
		if d != "" && !slices.Contains(out, filepath.Clean(d)) {
			out = append(out, filepath.Clean(d))
		}
	}
	return out
}

// PlanInit plans `acmd init`.
func PlanInit(in Input) (*Plan, error) {
	p := &Plan{}
	if in.ClientOnly {
		return p, planConfig(p, in)
	}
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

// PlanHooks plans putting acmd's hooks in each of HarnessDirs, and nothing
// else: for a Claude harness added after acmd init.
func PlanHooks(in Input) (*Plan, error) {
	p := &Plan{}
	for _, dir := range in.HarnessDirs {
		if err := planClaudeDir(p, in, filepath.Join(dir, "settings.json"), true); err != nil {
			return nil, err
		}
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
	for _, dir := range in.ClaudeDirs() {
		if err := planClaudeDir(p, in, filepath.Join(dir, "settings.json"), add); err != nil {
			return err
		}
	}
	return nil
}

func planClaudeDir(p *Plan, in Input, path string, add bool) error {
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
	settings := map[string]any{}
	c := Change{Path: in.ConfigFile, Exists: exists, Old: old, New: []byte(text), Mode: 0o644, Settings: settings}
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
	Version   string    `json:"version"`
	At        time.Time `json:"at"`
	AcmdPath  string    `json:"acmd_path"`
	ClaudeDir string    `json:"claude_dir"`
	// ClaudeDirs are the Claude harnesses' own folders it put hooks in too.
	ClaudeDirs []string `json:"claude_dirs,omitempty"`
	ConfigFile string   `json:"config_file"`
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

// StableBinary is the acmd path to write into hooks: the path acmd was
// invoked as (argv0, looked up on PATH if it's a
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
		// <prefix>/Cellar/acmd/<version>/bin/<name> → <prefix>/bin/<name>
		if link := filepath.Join(p[:i], "bin", filepath.Base(p)); isExecutable(link) {
			return link
		}
	}
	return filepath.Clean(p)
}
