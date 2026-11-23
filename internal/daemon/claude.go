package daemon

import (
	"context"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/reconcile"
)

// ClaudeCLI is Claude's CLI for one folder: what lists, stops and removes
// its background sessions (claudecli.CLI).
type ClaudeCLI interface {
	reconcile.Lister
	Stopper
	Remover
}

// claudeHarnesses is the Claude harnesses with their own folder, one per
// folder: each lists its own background sessions.
func (d *Daemon) claudeHarnesses(ctx context.Context) []core.Harness {
	seen := map[string]bool{d.cfg.ClaudeDir: true}
	var out []core.Harness
	for _, h := range d.harnessList(ctx) {
		if h.Kind != core.HarnessClaude || h.ConfigDir == "" {
			continue
		}
		if dir := d.claudeDir(h); !seen[dir] {
			seen[dir] = true
			out = append(out, h)
		}
	}
	return out
}

// claudeAgents lists Claude's sessions in every Claude folder, for the
// reconciler: Claude's standard folder (Config.Agents), then each harness's
// own. A folder whose listing fails is left out.
type claudeAgents struct{ d *Daemon }

func (c claudeAgents) Agents(ctx context.Context) ([]claudecli.Agent, error) {
	out, err := c.d.cfg.Agents.Agents(ctx)
	if err != nil || c.d.cfg.ClaudeFor == nil {
		return out, err
	}
	for _, h := range c.d.claudeHarnesses(ctx) {
		more, err := c.d.cfg.ClaudeFor(h).Agents(ctx)
		if err != nil {
			c.d.cfg.Log.Printf("claude agents (%s): %v", h.Name, err)
			continue
		}
		out = append(out, more...)
	}
	return out, nil
}

// claudeFor is Claude's CLI for a session's folder: its harness's, or
// Claude's standard folder's (Config.Stopper and Remover).
func (d *Daemon) claudeFor(ctx context.Context, s *core.Session) (Stopper, Remover) {
	if d.cfg.ClaudeFor != nil {
		if h := d.harnessOf(ctx, s.HarnessID); h != nil && h.Kind == core.HarnessClaude && h.ConfigDir != "" {
			cli := d.cfg.ClaudeFor(*h)
			return cli, cli
		}
	}
	return d.cfg.Stopper, d.cfg.Remover
}
