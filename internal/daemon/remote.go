package daemon

import (
	"context"
	"sync"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/store"
)

// RemoteChange is a session's Remote flag turned on or off by the user.
type RemoteChange struct {
	SessionID string
	Remote    bool
}

// remoteHooks are the functions OnRemoteChanged registered.
type remoteHooks struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(RemoteChange)
}

// OnRemoteChanged calls fn after each change to a session's Remote flag,
// once the change is saved, without d.mu held: fn may call the daemon. It
// isn't called when a session is created, or when nothing changed.
//
// The host relay (Remote sessions' Hosting) subscribes to it: when a session
// turns local (Remote false) it drops the clients attached to that session
// at once and stops listing it to them. The returned func unsubscribes.
func (d *Daemon) OnRemoteChanged(fn func(RemoteChange)) (stop func()) {
	h := &d.remoteHooks
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fns == nil {
		h.fns = map[int]func(RemoteChange){}
	}
	id := h.next
	h.next++
	h.fns[id] = fn
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.fns, id)
	}
}

func (d *Daemon) remoteChanged(c RemoteChange) {
	h := &d.remoteHooks
	h.mu.Lock()
	fns := make([]func(RemoteChange), 0, len(h.fns))
	for _, fn := range h.fns {
		fns = append(fns, fn)
	}
	h.mu.Unlock()
	for _, fn := range fns {
		fn(c)
	}
}

// setRemote turns a session's Remote flag on or off, logs it, and tells
// OnRemoteChanged's subscribers. Watchers see it through the next revision.
func (d *Daemon) setRemote(ctx context.Context, id string, on bool) error {
	d.mu.Lock()
	changed := false
	err := d.updateSession(ctx, id, func(s *core.Session) error {
		changed, s.Remote = s.Remote != on, on
		return nil
	})
	if err == nil && changed {
		detail := "off"
		if on {
			detail = "on"
		}
		err = d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: id, Kind: string(core.EventRemote), Detail: detail})
	}
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if changed {
		d.remoteChanged(RemoteChange{SessionID: id, Remote: on})
	}
	return nil
}

// newSessionsRemote is whether a new session is Remote when its request
// doesn't say: [remote] new_sessions_remote, read afresh from config.toml so
// a change applies without a restart. A config that doesn't load means no.
func (d *Daemon) newSessionsRemote(want *bool) bool {
	if want != nil {
		return *want
	}
	if d.cfg.ConfigFile == "" {
		return false
	}
	c, err := config.Load(d.cfg.ConfigFile)
	if err != nil {
		d.cfg.Log.Printf("config: %v (new sessions aren't Remote)", err)
		return false
	}
	return c.Remote.NewSessionsRemote
}

// remoteCount counts the Remote sessions that aren't archived.
func remoteCount(sessions []*core.Session) int {
	n := 0
	for _, s := range sessions {
		if s.Remote && s.Status != core.StatusArchived {
			n++
		}
	}
	return n
}
