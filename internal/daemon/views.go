package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// Claude starts and resumes background sessions (claudecli.CLI in production).
type Claude interface {
	StartBackground(ctx context.Context, o claudecli.StartOptions) (string, error)
	Resume(ctx context.Context, dir, sessionID string) (string, error)
}

// Views (SPEC §6.4): a view loop registers its pane, follows its Selected
// session by watching for changes, and proves it's alive through those
// watches. Views that stop watching are pruned; sessions are never touched.

const (
	maxWait     = 30 * time.Second
	viewTimeout = 90 * time.Second // no watch for this long: the view's pane is gone
)

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

// seen records each view's last watch (heartbeat).
type seen struct {
	mu    sync.Mutex
	at    map[string]time.Time
	start time.Time
}

func (s *seen) mark(id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at[id] = now
}

// last is when a view was last seen; views from before this daemon
// started count as seen at its start.
func (s *seen) last(id string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.at[id]; ok {
		return t
	}
	return s.start
}

func (s *seen) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.at, id)
}

func (d *Daemon) watch(ctx context.Context, req ipc.WatchRequest) (ipc.WatchResponse, error) {
	if req.ViewID != "" {
		d.seen.mark(req.ViewID, d.cfg.Now())
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
	resp := ipc.WatchResponse{Rev: rev}
	if req.ViewID != "" {
		v, err := d.store.View(ctx, req.ViewID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			resp.Gone = true
		case err != nil:
			return resp, err
		default:
			resp.View = v
		}
	}
	return resp, nil
}

// viewMutation handles the view.* mutations; d.mu is held.
func (d *Daemon) viewMutation(ctx context.Context, m ipc.Mutation) (any, error) {
	now := d.cfg.Now()
	switch m.Kind {
	case ipc.MutViewOpen:
		if m.Pane == nil {
			return nil, errors.New("view.open needs the pane")
		}
		sel := m.ID
		if sel == "" {
			s, err := d.mostRecentViewable(ctx)
			if err != nil {
				return nil, err
			}
			if s != nil {
				sel = s.ID
			}
		} else if _, err := d.viewable(ctx, sel); err != nil {
			return nil, err
		}
		v := &core.View{Pane: *m.Pane, LastFocusedAt: now}
		if err := d.store.PutView(ctx, v); err != nil {
			return nil, err
		}
		d.seen.mark(v.ID, now)
		if sel != "" {
			if err := d.show(ctx, v, sel); err != nil {
				return nil, err
			}
		}
		return v, nil
	case ipc.MutViewSelect:
		v, err := d.store.View(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		if m.Text != "" {
			if _, err := d.viewable(ctx, m.Text); err != nil {
				return nil, err
			}
		}
		v.LastFocusedAt = now
		return nil, d.show(ctx, v, m.Text)
	case ipc.MutViewClose:
		d.seen.forget(m.ID)
		err := d.store.DeleteView(ctx, m.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	case ipc.MutViewTouch:
		v, err := d.store.View(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		v.LastFocusedAt = now
		return nil, d.store.PutView(ctx, v)
	}
	return nil, fmt.Errorf("unknown mutation %q", m.Kind)
}

// show makes v show session id ("" = the placeholder) and marks it seen.
func (d *Daemon) show(ctx context.Context, v *core.View, id string) error {
	v.Selected = id
	if err := d.store.PutView(ctx, v); err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	return d.updateSession(ctx, id, func(s *core.Session) error {
		s.Unseen = false
		return nil
	})
}

// viewable returns a session a view can show: a non-archived background
// session (external sessions live in the user's own pane, SPEC §6.3).
func (d *Daemon) viewable(ctx context.Context, id string) (*core.Session, error) {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return nil, err
	}
	switch {
	case s.Status == core.StatusArchived:
		return nil, errors.New("that session is archived (acmd unarchive first)")
	case s.Kind != core.KindBackground:
		return nil, errors.New("that session runs in your own pane, so a view can't show it")
	case s.BgID == "":
		return nil, errors.New("that session has no Claude background id yet")
	}
	return s, nil
}

// mostRecentViewable is the session a new view starts on (SPEC §6.4).
func (d *Daemon) mostRecentViewable(ctx context.Context) (*core.Session, error) {
	sessions, err := d.store.Sessions(ctx, false)
	if err != nil {
		return nil, err
	}
	var best *core.Session
	for _, s := range sessions {
		if s.Kind != core.KindBackground || s.BgID == "" {
			continue
		}
		if best == nil || activity(s).After(activity(best)) {
			best = s
		}
	}
	return best, nil
}

func activity(s *core.Session) time.Time {
	if s.LastEventAt.After(s.CreatedAt) {
		return s.LastEventAt
	}
	return s.CreatedAt
}

// shown reports whether a live view shows the session (so a status change
// isn't unseen); d.mu is held.
func (d *Daemon) shown(ctx context.Context, id string) bool {
	views, err := d.store.Views(ctx)
	if err != nil {
		return false
	}
	for _, v := range views {
		if v.Selected == id && d.cfg.Now().Sub(d.seen.last(v.ID)) < viewTimeout {
			return true
		}
	}
	return false
}

// pruneViews removes views whose loop stopped watching (pane closed
// without `q`, terminal quit, crash).
func (d *Daemon) pruneViews(ctx context.Context) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	views, err := d.store.Views(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range views {
		if d.cfg.Now().Sub(d.seen.last(v.ID)) < viewTimeout {
			continue
		}
		if err := d.store.DeleteView(ctx, v.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return n, err
		}
		d.seen.forget(v.ID)
		n++
	}
	return n, nil
}

// newSession starts `claude --bg` (outside the lock: it takes a second or
// two and hooks must not wait) and records the session.
func (d *Daemon) newSession(ctx context.Context, req ipc.NewRequest) (*core.Session, error) {
	if d.cfg.Claude == nil {
		return nil, errors.New("claude not available: run `acmd doctor`")
	}
	if req.Cwd == "" {
		return nil, errors.New("no folder for the new session")
	}
	bgID, err := d.cfg.Claude.StartBackground(ctx, claudecli.StartOptions{Dir: req.Cwd, Name: req.Name, Args: req.Args, Prompt: req.Prompt})
	if errors.Is(err, claudecli.ErrNotTrusted) {
		return nil, errors.New(ipc.ErrNotTrustedText)
	}
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	// Its first hook may have beaten us here and created the row already.
	s, err := d.store.SessionByClaudePrefix(ctx, bgID)
	created := errors.Is(err, store.ErrNotFound)
	switch {
	case created:
		s = &core.Session{Kind: core.KindBackground, Cwd: req.Cwd, CreatedAt: d.cfg.Now().UTC(), NameSource: core.NameAuto}
		core.Apply(s, core.Event{Kind: core.EventCreated, Time: d.cfg.Now()})
	case err != nil:
		return nil, err
	}
	s.BgID = bgID
	if req.GroupID != "" {
		s.GroupID = req.GroupID
	}
	if req.Name != "" {
		s.Name, s.NameSource = req.Name, core.NameUser
	}
	if req.Description != "" {
		s.Description = req.Description
	}
	if created {
		err = d.store.CreateSession(ctx, s)
	} else {
		err = d.store.UpdateSession(ctx, s)
	}
	if err != nil {
		return nil, err
	}
	if req.ShowIn != "" {
		v, err := d.store.View(ctx, req.ShowIn)
		if err == nil {
			v.LastFocusedAt = d.cfg.Now()
			err = d.show(ctx, v, s.ID)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return s, nil
}

// resume restarts a paused background session with `claude --bg --resume`
// (SPEC §6.2; Claude keeps the session id, M0 C3). Attaching restarts a
// stopped session too; this is the fallback and `acmd resume`.
func (d *Daemon) resume(ctx context.Context, id string) (*core.Session, error) {
	if d.cfg.Claude == nil {
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
	case s.ClaudeSessionID == "":
		return nil, errors.New("that session has no Claude conversation to resume")
	case s.Status != core.StatusPaused:
		return s, nil // already running
	}
	bgID, err := d.cfg.Claude.Resume(ctx, s.Cwd, s.ClaudeSessionID)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, err = d.store.Session(ctx, id); err != nil {
		return nil, err
	}
	s.BgID, s.Kind, s.External = bgID, core.KindBackground, nil
	core.Apply(s, core.Event{Kind: core.EventResumed, Time: d.eventTime(s)})
	return s, d.store.UpdateSession(ctx, s)
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
