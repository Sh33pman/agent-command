package doctor

import (
	"context"
	"path/filepath"

	"github.com/brettmostert/agent-command/internal/setup"
)

// AgentHooks is one agent's hook setup, for the app's warnings: whether the
// agent is installed, and what is wrong with its hooks. An agent that isn't
// installed has nothing to set up, so it never has problems.
type AgentHooks struct {
	Agent     string    `json:"agent"` // "claude" or "antigravity"
	Name      string    `json:"name"`  // what to call it: "Claude Code", "Antigravity"
	Installed bool      `json:"installed"`
	Problems  []Problem `json:"problems,omitempty"`
}

// Problem is a check that needs doing something about.
type Problem struct {
	Severity string `json:"severity"` // "warn" or "fail"
	Text     string `json:"text"`
	Fix      string `json:"fix,omitempty"`
}

// Agents checks each agent's hooks. Missing hooks count as a problem even if
// `acmd init` never ran: the app shows the agent isn't set up.
func Agents(ctx context.Context, e Env) []AgentHooks {
	claude := AgentHooks{Agent: "claude", Name: secClaude}
	if _, err := e.LookPath("claude"); err == nil {
		claude.Installed = true
		r := &Report{}
		checkClaude(ctx, r, e, setup.Record{}, true)
		settings, _ := e.Read(filepath.Join(setup.ClaudeConfigDir(e.Home, e.Getenv), "settings.json"))
		paths, _ := setup.HookPaths(settings)
		claude.Problems = append(problems(r), notThisAcmd(e, paths)...)
	}

	agy := AgentHooks{Agent: "antigravity", Name: secAgy}
	if _, err := e.LookPath("agy"); err == nil {
		agy.Installed = true
		r := &Report{}
		checkAntigravity(r, e, true)
		file, _ := e.Read(filepath.Join(setup.AntigravityConfigDir(e.Home), "hooks.json"))
		agy.Problems = append(problems(r), notThisAcmd(e, setup.AntigravityHooks(file).Paths)...)
	}
	return []AgentHooks{claude, agy}
}

// problems are a report's warnings and failures.
func problems(r *Report) []Problem {
	var out []Problem
	for _, c := range r.Checks {
		switch c.Status {
		case Warn:
			out = append(out, Problem{Severity: "warn", Text: c.Text, Fix: c.Fix})
		case Fail:
			out = append(out, Problem{Severity: "fail", Text: c.Text, Fix: c.Fix})
		}
	}
	return out
}

// notThisAcmd is a problem when every hook runs an acmd that works, but not
// the one installed now (an old copy, a build folder): its sessions would be
// tracked by the wrong version. A missing binary is already reported.
func notThisAcmd(e Env, hookPaths []string) []Problem {
	if len(hookPaths) != 1 || e.AcmdPath == "" || !e.executable(hookPaths[0]) {
		return nil
	}
	if samePath(e, hookPaths[0], e.AcmdPath) {
		return nil
	}
	return []Problem{{Severity: "warn", Fix: fixInit,
		Text: "hooks run " + setup.Tilde(hookPaths[0], e.Home) + ", not this acmd (" + setup.Tilde(e.AcmdPath, e.Home) + ")"}}
}

func samePath(e Env, a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	if e.Resolve == nil {
		return false
	}
	ra, errA := e.Resolve(a)
	rb, errB := e.Resolve(b)
	return errA == nil && errB == nil && ra == rb
}
