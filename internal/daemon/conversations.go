package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/brettmostert/agent-command/internal/agycli"
	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// maxConversations limits the list of earlier sessions to resume.
const maxConversations = 300

// conversations lists earlier Claude and Antigravity conversations that can be
// resumed, the most recently active first.
func (d *Daemon) conversations(ctx context.Context) ([]ipc.Conversation, error) {
	found, err := claudecli.Conversations(d.cfg.ClaudeDir, maxConversations)
	if err != nil {
		return nil, err
	}
	out := make([]ipc.Conversation, 0, len(found))
	for _, c := range found {
		out = append(out, ipc.Conversation{ID: c.ID, Harness: core.HarnessClaude, Cwd: c.Cwd, Title: c.Title, Branch: c.Branch, UpdatedAt: c.UpdatedAt})
	}
	if d.cfg.AgyDir != "" {
		agy, err := agycli.Conversations(d.cfg.AgyDir, maxConversations)
		if err != nil { // agy's history is extra: Claude's list still stands
			d.cfg.Log.Printf("list Antigravity conversations: %v", err)
		}
		for _, c := range agy {
			out = append(out, ipc.Conversation{ID: c.ID, Harness: core.HarnessAntigravity, Cwd: c.Cwd, Title: c.Title, UpdatedAt: c.UpdatedAt})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
		if len(out) > maxConversations {
			out = out[:maxConversations]
		}
	}
	running := d.runningConversations(ctx) // Claude's: agy has no such list
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, c := range out {
		out[i].Running = running[c.ID]
		if s, err := d.store.SessionByClaudeID(ctx, c.ID); err == nil && s.Kind == core.KindHosted {
			out[i].SessionID = s.ID
		}
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
	c, err := d.findConversation(req)
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
	req.Cwd, req.Resume, req.Harness = c.Cwd, c.ID, c.Harness
	if req.Name == "" {
		req.Name = c.Title
	}
	return d.hostSession(ctx, req)
}

// findConversation finds the conversation a request resumes: agy's when it
// asks for Antigravity, else Claude's.
func (d *Daemon) findConversation(req ipc.NewRequest) (ipc.Conversation, error) {
	switch req.Harness {
	case core.HarnessAntigravity:
		c, err := agycli.FindConversation(d.cfg.AgyDir, req.Resume)
		return ipc.Conversation{ID: c.ID, Harness: core.HarnessAntigravity, Cwd: c.Cwd, Title: c.Title}, err
	case "", core.HarnessClaude:
		c, err := claudecli.FindConversation(d.cfg.ClaudeDir, req.Resume)
		return ipc.Conversation{ID: c.ID, Harness: core.HarnessClaude, Cwd: c.Cwd, Title: c.Title}, err
	}
	return ipc.Conversation{}, fmt.Errorf("%s has no conversations to resume", req.Harness)
}
