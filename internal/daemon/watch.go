package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

const maxWait = 30 * time.Second

// changes is a revision counter clients can wait on.
type changes struct {
	mu  sync.Mutex
	rev uint64
	ch  chan struct{}
}

func (c *changes) bump() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rev++
	if c.ch != nil {
		close(c.ch)
	}
	c.ch = make(chan struct{})
}

func (c *changes) current() (uint64, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ch == nil {
		c.ch = make(chan struct{})
	}
	return c.rev, c.ch
}

func (d *Daemon) watch(ctx context.Context, req ipc.WatchRequest) (ipc.WatchResponse, error) {
	if req.App {
		d.appWatches.Add(1)
		defer func() {
			d.appLastWatch.Store(d.cfg.Now().UnixNano())
			d.appWatches.Add(-1)
		}()
	}
	wait := time.Duration(req.WaitMS) * time.Millisecond
	if wait > maxWait {
		wait = maxWait
	}
	rev, ch := d.changes.current()
	if rev == req.Rev && wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ch:
		case <-t.C:
		case <-ctx.Done():
		case <-d.stop:
		}
		t.Stop()
		rev, _ = d.changes.current()
	}
	return ipc.WatchResponse{Rev: rev}, nil
}

// newSession starts a hosted session.
func (d *Daemon) newSession(ctx context.Context, req ipc.NewRequest) (*core.Session, error) {
	if d.cfg.Hosts == nil {
		return nil, errors.New("claude not available: run `acmd doctor`")
	}
	if req.Resume != "" {
		return d.resumeConversation(ctx, req)
	}
	if req.Cwd == "" {
		return nil, errors.New("no folder for the new session")
	}
	return d.hostSession(ctx, req)
}

// resume starts a stopped session again in a new host with `claude
// --resume`. A session from before hosting (a `claude --bg`
// one) is stopped first if it still runs (Claude keeps the conversation)
// and becomes a hosted session; so does one that ended in the user's own
// pane (acmd adopt).
func (d *Daemon) resume(ctx context.Context, id string) (*core.Session, error) {
	if d.cfg.Hosts == nil {
		return nil, errors.New("claude not available: run `acmd doctor`")
	}
	d.mu.Lock()
	s, err := d.store.Session(ctx, id)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	switch {
	case s.Status == core.StatusArchived:
		return nil, errors.New("that session is archived (acmd unarchive first)")
	case s.ClaudeSessionID == "" && !s.IsTerminal():
		return nil, errors.New("that session has no Claude conversation to resume")
	case s.Kind == core.KindHosted && !s.Stopped():
		return s, nil // already running
	case s.Kind == core.KindExternal && !s.Stopped():
		return nil, errors.New("that session runs in your own pane: exit Claude there, or move it into acmd")
	case s.Kind == core.KindBackground && !s.Stopped() && s.BgID != "" && d.cfg.Stopper != nil:
		if err := d.cfg.Stopper.Stop(ctx, s.BgID); err != nil {
			return nil, fmt.Errorf("stop %s: %w", s.BgID, err)
		}
	}
	return d.resumeHosted(ctx, s, "", nil)
}

// eventTime is the time for an event acmd itself applies to s: now, but
// never before s's last event, which the state machine would drop as stale
// (hook timestamps come from other processes).
func (d *Daemon) eventTime(s *core.Session) time.Time {
	if now := d.cfg.Now(); !now.Before(s.LastEventAt) {
		return now
	}
	return s.LastEventAt
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("bad request: %w", err)
	}
	return v, nil
}
