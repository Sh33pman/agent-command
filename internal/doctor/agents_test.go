package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
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

func healthIDs(h []ipc.Health) string {
	var ids []string
	for _, e := range h {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

func TestHealth(t *testing.T) {
	f := newFake(t)
	ctx := context.Background()
	if h := Health(ctx, f.Env()); len(h) != 0 {
		t.Fatalf("healthy: %+v", h)
	}

	// No hooks anywhere, with agy installed: an offer naming both, and each agent's warning.
	f.write(filepath.Join(f.home, "bin", "agy"), "#!/bin/sh\n", 0o755)
	f.write(filepath.Join(f.home, ".claude/settings.json"), `{"model":"opus"}`, 0o600)
	h := Health(ctx, f.Env())
	if healthIDs(h) != "hooks.claude,hooks.antigravity,setup.offer" {
		t.Fatalf("ids = %s", healthIDs(h))
	}
	if h[0].Severity != ipc.HealthError || h[0].Title != "Claude Code's hooks aren't set up" || h[0].Fix.Op != ipc.OpInit ||
		!strings.Contains(h[0].Detail, "No acmd hooks") {
		t.Errorf("claude = %+v", h[0])
	}
	if !strings.Contains(h[2].Detail, "your Claude Code settings and Antigravity's hooks.json") {
		t.Errorf("offer = %+v", h[2])
	}

	// Hooks that run another acmd that works: set up again, without asking.
	f2 := newFake(t)
	other := filepath.Join(f2.home, "old", "acmd")
	f2.write(other, "#!/bin/sh\n", 0o755)
	settings, _, err := setup.AddHooks([]byte(`{}`), other)
	must(t, err)
	f2.write(filepath.Join(f2.home, ".claude/settings.json"), string(settings), 0o600)
	h = Health(ctx, f2.Env())
	if !strings.HasSuffix(healthIDs(h), ipc.HealthSetupRefresh) {
		t.Errorf("stale: %s", healthIDs(h))
	}
	if sentence("") != "" || sentence("ok.") != "Ok." {
		t.Error("sentence")
	}
}
