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
	"github.com/brettmostert/agent-command/internal/store"
)

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
	if req.ViewID != "" && !req.Passive {
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
			resp.Note = d.notes.get(req.ViewID)
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
	case ipc.MutViewNote:
		if m.Text == "" {
			d.notes.set(m.ID, nil)
		} else {
			d.notes.set(m.ID, &ipc.ViewNote{Text: m.Text, Err: m.Flag})
		}
		return nil, nil
	case ipc.MutViewClose:
		d.seen.forget(m.ID)
		d.notes.set(m.ID, nil)
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
	case ipc.MutViewHidden:
		v, err := d.store.View(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		v.SidebarHidden = m.Flag
		return nil, d.store.PutView(ctx, v)
	case ipc.MutViewSidebar:
		v, err := d.store.View(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		v.Sidebar = core.PaneLoc{}
		if m.Pane != nil {
			v.Sidebar = *m.Pane
		}
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
	case !s.Managed():
		return nil, errors.New("that session runs in your own pane, so a view can't show it")
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
		if !s.Managed() {
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
		d.notes.set(v.ID, nil)
		n++
	}
	return n, nil
}

// newSession starts a hosted session (SPEC §4.5).
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
// --resume` (SPEC §6.2). A session from before hosting (a `claude --bg`
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
	return d.resumeHosted(ctx, s)
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

// notes holds each view's status bar message. They are short-lived (any
// key in the sidebar clears them), so they aren't persisted.
type notes struct {
	mu sync.Mutex
	m  map[string]*ipc.ViewNote
}

func (n *notes) set(view string, note *ipc.ViewNote) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if note == nil {
		delete(n.m, view)
		return
	}
	if n.m == nil {
		n.m = map[string]*ipc.ViewNote{}
	}
	n.m[view] = note
}

func (n *notes) get(view string) *ipc.ViewNote {
	n.mu.Lock()
	defer n.mu.Unlock()
	if x := n.m[view]; x != nil {
		c := *x
		return &c
	}
	return nil
}
