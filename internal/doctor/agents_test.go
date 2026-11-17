package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/setup"
)

func agentsOf(t *testing.T, f *fake) map[string]AgentHooks {
	t.Helper()
	out := map[string]AgentHooks{}
	for _, a := range Agents(context.Background(), f.Env()) {
		out[a.Agent] = a
	}
	return out
}

func TestAgentsHealthy(t *testing.T) {
	f := newFake(t)
	got := agentsOf(t, f)
	if c := got["claude"]; !c.Installed || len(c.Problems) != 0 {
		t.Errorf("claude = %+v", c)
	}
	// agy isn't installed: nothing to check, so nothing wrong.
	if a := got["antigravity"]; a.Installed || len(a.Problems) != 0 {
		t.Errorf("antigravity = %+v", a)
	}
}

// An agent that isn't installed is never a problem, whatever its files say.
func TestAgentsNotInstalled(t *testing.T) {
	f := newFake(t)
	must(t, os.Remove(filepath.Join(f.home, "bin", "claude")))
	must(t, os.Remove(filepath.Join(f.home, ".claude/settings.json")))
	if c := agentsOf(t, f)["claude"]; c.Installed || len(c.Problems) != 0 {
		t.Errorf("claude not installed = %+v", c)
	}
}

func TestAgentsProblems(t *testing.T) {
	f := newFake(t)
	f.write(filepath.Join(f.home, "bin", "agy"), "#!/bin/sh\n", 0o755)

	// agy installed with no hooks; Claude's settings lost theirs.
	f.write(filepath.Join(f.home, ".claude/settings.json"), `{"model":"opus"}`, 0o600)
	got := agentsOf(t, f)
	if a := got["antigravity"]; !a.Installed || len(a.Problems) != 1 || !strings.Contains(a.Problems[0].Text, "no acmd hooks") || a.Problems[0].Fix == "" {
		t.Errorf("agy without hooks = %+v", a)
	}
	if c := got["claude"]; len(c.Problems) != 1 || c.Problems[0].Severity != "fail" || !strings.Contains(c.Problems[0].Text, "no acmd hooks") {
		t.Errorf("claude without hooks = %+v", c)
	}

	// Hooks that run another acmd that works: an old copy, say.
	other := filepath.Join(f.home, "old", "acmd")
	f.write(other, "#!/bin/sh\n", 0o755)
	settings, _, err := setup.AddHooks([]byte(`{}`), other)
	must(t, err)
	f.write(filepath.Join(f.home, ".claude/settings.json"), string(settings), 0o600)
	hooks, _, err := setup.AddAntigravityHooks(nil, other)
	must(t, err)
	f.write(filepath.Join(f.home, ".gemini/config/hooks.json"), string(hooks), 0o644)
	got = agentsOf(t, f)
	for _, name := range []string{"claude", "antigravity"} {
		p := got[name].Problems
		if len(p) != 1 || p[0].Severity != "warn" || !strings.Contains(p[0].Text, "not this acmd") {
			t.Errorf("%s with another acmd = %+v", name, p)
		}
	}
}
