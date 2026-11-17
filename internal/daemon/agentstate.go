package daemon

import (
	"cmp"
	"context"
	"errors"
	"maps"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// agentStateOnce applies what running Antigravity sessions last reported over
// OSC 7501 (the host reads it from agy's output). agy has no hook for waiting
// on you, a permission prompt or a question, but it reports one there; its
// screen then shows the answers, which make the Inbox item's options. The
// hosts are asked without d.mu held. It reports whether anything changed.
func (d *Daemon) agentStateOnce(ctx context.Context) (bool, error) {
	d.mu.Lock()
	sessions, err := d.store.Sessions(ctx, false)
	seen := maps.Clone(d.agentSeen)
	d.mu.Unlock()
	if err != nil {
		return false, err
	}
	reports, screens := map[string]term.AgentState{}, map[string]string{}
	for _, s := range sessions {
		if s.IsAntigravity() && s.Kind == core.KindHosted && !s.Stopped() && s.Status != core.StatusArchived {
			if st, ok := d.cfg.HostAgent(s.ID); ok && st.Seq > 0 {
				reports[s.ID] = st
				if st.Seq != seen[s.ID] && st.State == "blocked" && d.cfg.HostScreen != nil {
					if text, ok := d.cfg.HostScreen(s.ID); ok {
						screens[s.ID] = text
					}
				}
			}
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.agentSeen == nil {
		d.agentSeen = map[string]uint64{}
	}
	changed := false
	for id, st := range reports {
		if st.Seq == d.agentSeen[id] {
			continue
		}
		d.agentSeen[id] = st.Seq
		e, ok := agentStateEvent(st)
		if !ok {
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
		// agy's Stop hook usually ends the turn first, with no text; its done report
		// then changes no status, but it brings the reply, so it still makes the
		// Inbox's done item.
		reply := e.Kind == core.HookStop && e.LastMessage != "" && tr.Applied
		// A block is a new prompt even when the session was waiting already.
		var ask *core.InboxItem
		if e.Kind == core.HookNotification && tr.Applied {
			ask = core.AntigravityAsk(s, e, st.Kind, screens[id])
		}
		if !tr.Changed() && !reply && ask == nil {
			continue
		}
		err = d.store.WithTx(ctx, func(tx *store.Store) error {
			if err := applyInbox(ctx, tx, s, e); err != nil {
				return err
			}
			if ask != nil {
				if err := tx.AddInboxItem(ctx, *ask); err != nil {
					return err
				}
			}
			if err := tx.UpdateSession(ctx, s); err != nil {
				return err
			}
			return tx.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
				Kind: "antigravity:state", Detail: string(s.Status) + " " + st.State + " " + st.Kind})
		})
		if err != nil {
			return changed, err
		}
		changed = true
		d.transition(s, tr)
	}
	return changed, nil
}

// agentStateEvent is the event an OSC 7501 report stands for, in the words the
// state machine already knows: working is busy, blocked is waiting on you (its
// message says what for), done and idle end the turn (done's message is agy's
// reply), failed is an error.
func agentStateEvent(st term.AgentState) (core.Event, bool) {
	e := core.Event{Time: st.At, Agent: "Antigravity"}
	switch st.State {
	case "working":
		e.Kind = core.HookPreToolUse
	case "blocked":
		e.Kind, e.NotificationType = core.HookNotification, "agent_needs_input"
		e.Message = cmp.Or(st.Msg, blockedWhat(st.Kind))
	case "done", "idle":
		e.Kind, e.LastMessage = core.HookStop, st.Msg // done carries agy's reply
	case "failed":
		e.Kind, e.Error = core.HookStopFailure, cmp.Or(st.Msg, "an error")
	default:
		return core.Event{}, false
	}
	return e, true
}

// blockedWhat words a block that came with no message.
func blockedWhat(kind string) string {
	switch kind {
	case "permission":
		return "permission"
	case "question":
		return "question"
	case "plan":
		return "plan to review"
	case "auth":
		return "sign in"
	}
	return "waiting for you"
}
