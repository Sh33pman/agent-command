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

// conversations lists earlier conversations that can be resumed, the most
// recently active first, each in the harness whose history it's in: a Claude
// harness's come from its own folder only (two on one folder list it once),
// and agy's, which has one history, are its first harness's.
func (d *Daemon) conversations(ctx context.Context) ([]ipc.Conversation, error) {
	var out []ipc.Conversation
	seen := map[string]bool{}
	agyDone := false
	for _, h := range d.harnessList(ctx) {
		switch h.Kind {
		case core.HarnessClaude:
			dir := d.claudeDir(h)
			if seen[dir] || dir == "" {
				continue
			}
			seen[dir] = true
			found, err := claudecli.Conversations(dir, maxConversations)
			if err != nil {
				d.cfg.Log.Printf("list %s's conversations: %v", h.Name, err)
				continue
			}
			for _, c := range found {
				out = append(out, ipc.Conversation{ID: c.ID, HarnessID: h.ID, HarnessKind: h.Kind, Cwd: c.Cwd, Title: c.Title, Branch: c.Branch, UpdatedAt: c.UpdatedAt})
			}
		case core.HarnessAntigravity:
			if agyDone || d.cfg.AgyDir == "" {
				continue
			}
			agyDone = true
			agy, err := agycli.Conversations(d.cfg.AgyDir, maxConversations)
			if err != nil {
				d.cfg.Log.Printf("list Antigravity conversations: %v", err)
			}
			for _, c := range agy {
				out = append(out, ipc.Conversation{ID: c.ID, HarnessID: h.ID, HarnessKind: h.Kind, Cwd: c.Cwd, Title: c.Title, UpdatedAt: c.UpdatedAt})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > maxConversations {
		out = out[:maxConversations]
	}
	if out == nil {
		out = []ipc.Conversation{}
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
	agents, err := claudeAgents{d}.Agents(ctx)
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
	c, err := d.findConversation(ctx, req)
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
	req.Cwd, req.Resume, req.HarnessID, req.HarnessKind = c.Cwd, c.ID, c.HarnessID, c.HarnessKind
	if req.Name == "" {
		req.Name = c.Title
	}
	return d.hostSession(ctx, req)
}

// findConversation finds the conversation a request resumes, in its
// harness's history (the default harness's when it names none).
func (d *Daemon) findConversation(ctx context.Context, req ipc.NewRequest) (ipc.Conversation, error) {
	h, err := d.newHarness(ctx, req.HarnessID, req.HarnessKind)
	if err != nil {
		return ipc.Conversation{}, err
	}
	if h == nil {
		return ipc.Conversation{}, fmt.Errorf("a terminal has no conversations to resume")
	}
	switch h.Kind {
	case core.HarnessAntigravity:
		c, err := agycli.FindConversation(d.cfg.AgyDir, req.Resume)
		return ipc.Conversation{ID: c.ID, HarnessID: h.ID, HarnessKind: h.Kind, Cwd: c.Cwd, Title: c.Title}, err
	default:
		c, err := claudecli.FindConversation(d.claudeDir(*h), req.Resume)
		return ipc.Conversation{ID: c.ID, HarnessID: h.ID, HarnessKind: h.Kind, Cwd: c.Cwd, Title: c.Title}, err
	}
}
