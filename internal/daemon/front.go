package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// unknownAfter is how long an agent is in front of a terminal, with no hook
// from it, before its row says unknown. Claude's SessionStart comes within a
// second or two. Tests shorten it.
var unknownAfter = 5 * time.Second

// frontOnce asks each running terminal what's in front, the fallback for an
// agent whose hooks are missing (docs/v2/architecture.md §5): an agent with no
// hooks shows unknown, and the shell back in front ends whatever agent the row
// showed. The hosts are asked without d.mu held. It reports whether anything changed.
func (d *Daemon) frontOnce(ctx context.Context) (bool, error) {
	d.mu.Lock()
	sessions, err := d.store.Sessions(ctx, false)
	d.mu.Unlock()
	if err != nil {
		return false, err
	}
	fronts := map[string]term.Front{}
	for _, s := range sessions {
		if s.IsTerminal() && s.Kind == core.KindHosted && !s.Stopped() && s.Status != core.StatusArchived {
			if f, ok := d.cfg.HostFront(s.ID); ok {
				fronts[s.ID] = f
			}
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.cfg.Now()
	since := d.agentSince
	d.agentSince = map[string]time.Time{}
	changed := false
	for id, f := range fronts {
		e := core.Event{Kind: core.EventForeground, Front: f.Name, Time: now}
		switch {
		case f.Shell:
			e.Front = core.FrontShell
		case core.IsAgent(f.Name):
			first, ok := since[id]
			if !ok {
				first = now
			}
			d.agentSince[id] = first
			if now.Sub(first) < unknownAfter {
				continue
			}
		default:
			continue
		}
		s, err := d.store.Session(ctx, id) // fresh: a hook may have come meanwhile
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return changed, err
		}
		tr := core.Apply(s, e)
		if !tr.Changed() {
			continue
		}
		d.transition(s, tr)
		if err := d.applyInbox(ctx, s, e); err != nil {
			return changed, err
		}
		changed = true
		if err := d.store.UpdateSession(ctx, s); err != nil {
			return changed, err
		}
		if err := d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID,
			Kind: string(core.EventForeground), Detail: string(s.Status) + " " + e.Front}); err != nil {
			return changed, err
		}
	}
	return changed, nil
}
