package doctor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Health is the setup's part of the daemon's health list: each installed
// agent whose hooks need attention, and whether acmd should offer to set up
// (hooks missing) or set up again by itself (the hooks run another acmd).
// `acmd doctor --json` prints the daemon's list, so both say the same.
func Health(ctx context.Context, e Env) []ipc.Health {
	var out []ipc.Health
	initFix := func(title string) *ipc.HealthFix {
		return &ipc.HealthFix{Op: ipc.OpInit, Title: title, Step: "Run acmd init."}
	}
	for _, a := range Agents(ctx, e) {
		if !a.Installed || len(a.Problems) == 0 {
			continue
		}
		severity, title := ipc.HealthWarning, a.Name+"'s hooks need a look"
		var lines []string
		for _, p := range a.Problems {
			if p.Severity == "fail" {
				severity, title = ipc.HealthError, a.Name+"'s hooks aren't set up"
			}
			lines = append(lines, sentence(p.Text))
		}
		out = append(out, ipc.Health{ID: ipc.HealthHooks + a.Agent, Severity: severity, Title: title,
			Detail: "Its sessions can't show live status, and the Inbox misses them. " + strings.Join(lines, " "),
			Fix:    initFix("Set Up")})
	}

	need := setupNeed(e)
	switch {
	case len(need.missing) > 0:
		out = append(out, ipc.Health{ID: ipc.HealthSetupOffer, Severity: ipc.HealthWarning, Title: "Set up Agent Command?",
			Detail: "This adds hook entries to " + strings.Join(need.missing, " and ") + ", so sessions can show when they're busy, " +
				"waiting or done. It backs the file up first, and keeps your other settings. It also saves your login shell's " +
				"environment for the sessions it starts.",
			Fix: initFix("Set Up")})
	case need.stale:
		out = append(out, ipc.Health{ID: ipc.HealthSetupRefresh, Severity: ipc.HealthInfo, Title: "Setting up again",
			Detail: "The hooks run an acmd from before it moved.", Fix: initFix("Set Up Again")})
	}
	return out
}

type need struct {
	missing []string // where hooks are missing: "your Claude Code settings", "Antigravity's hooks.json"
	stale   bool     // hooks are there, but run another acmd
}

// setupNeed reads the agents' hook files: Claude Code's always (it's what
// acmd is for), agy's when it's installed.
func setupNeed(e Env) need {
	var n need
	settings, _ := e.Read(filepath.Join(setup.ClaudeConfigDir(e.Home, e.Getenv), "settings.json"))
	claude, _ := setup.HookPaths(settings)
	if len(claude) == 0 {
		n.missing = append(n.missing, "your Claude Code settings")
	} else if !e.runsThis(claude) {
		n.stale = true
	}
	for _, dir := range harnessDirs(e) {
		b, _ := e.Read(filepath.Join(dir, "settings.json"))
		if paths, _ := setup.HookPaths(b); len(paths) == 0 {
			n.missing = append(n.missing, "the Claude settings in "+setup.Tilde(dir, e.Home))
		} else if !e.runsThis(paths) {
			n.stale = true
		}
	}
	if _, err := e.LookPath("agy"); err == nil {
		file, _ := e.Read(filepath.Join(setup.AntigravityConfigDir(e.Home), "hooks.json"))
		agy := setup.AntigravityHooks(file).Paths
		if len(agy) == 0 {
			n.missing = append(n.missing, "Antigravity's hooks.json")
		} else if !e.runsThis(agy) {
			n.stale = true
		}
	}
	return n
}

// runsThis says whether the hooks run this acmd.
func (e Env) runsThis(paths []string) bool {
	for _, p := range paths {
		if e.AcmdPath != "" && samePath(e, p, e.AcmdPath) {
			return true
		}
	}
	return false
}

func sentence(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// harnessDirs are the Claude harnesses' own folders acmd put hooks in, as
// setup.json records them.
func harnessDirs(e Env) []string {
	b, err := e.Read(e.Paths.Setup())
	if err != nil {
		return nil
	}
	var rec setup.Record
	if json.Unmarshal(b, &rec) != nil {
		return nil
	}
	return rec.ClaudeDirs
}
