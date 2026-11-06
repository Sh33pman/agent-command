package daemon

import (
	"context"
	"errors"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// maxConversations limits the list of earlier sessions to resume.
const maxConversations = 300

// conversations lists earlier Claude sessions that can be resumed.
func (d *Daemon) conversations(ctx context.Context) ([]ipc.Conversation, error) {
	found, err := claudecli.Conversations(d.cfg.ClaudeDir, maxConversations)
	if err != nil {
		return nil, err
	}
	running := d.runningConversations(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ipc.Conversation, 0, len(found))
	for _, c := range found {
		item := ipc.Conversation{ID: c.ID, Cwd: c.Cwd, Title: c.Title, Branch: c.Branch, UpdatedAt: c.UpdatedAt, Running: running[c.ID]}
		if s, err := d.store.SessionByClaudeID(ctx, c.ID); err == nil && s.Kind == core.KindHosted {
			item.SessionID = s.ID
		}
		out = append(out, item)
	}
	return out, nil
}

// runningConversations is what Claude reports as running. It is best effort:
// without it, the list just can't warn about a conversation that is still live.
func (d *Daemon) runningConversations(ctx context.Context) map[string]bool {
	running := map[string]bool{}
	if d.cfg.Agents == nil {
		return running
	}
	agents, err := d.cfg.Agents.Agents(ctx)
	if err != nil {
		d.cfg.Log.Printf("list running conversations: %v", err)
		return running
	}
	for _, a := range agents {
		if !a.Stopped() {
			running[a.SessionID] = true
		}
	}
	return running
}

// resumeConversation continues an earlier conversation in a host, and tracks it
// from then on. One acmd already knows is that session, not a second one.
func (d *Daemon) resumeConversation(ctx context.Context, req ipc.NewRequest) (*core.Session, error) {
	c, err := claudecli.FindConversation(d.cfg.ClaudeDir, req.Resume)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	s, err := d.store.SessionByClaudeID(ctx, c.ID)
	if err == nil && s.Status == core.StatusArchived {
		core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
		err = d.store.UpdateSession(ctx, s)
	}
	d.mu.Unlock()
	switch {
	case err == nil:
		return d.resume(ctx, s.ID)
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	req.Cwd, req.Resume, req.Harness = c.Cwd, c.ID, core.HarnessClaude
	if req.Name == "" {
		req.Name = c.Title
	}
	return d.hostSession(ctx, req)
}
