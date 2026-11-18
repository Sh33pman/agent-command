package hostapi

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Events: each client's stream follows the daemon's revision. On each change
// it reads the state, compares what the client may see with what it saw last,
// and queues the difference. A client that reads slowly never holds up the
// daemon: its queue is bounded, and when it fills, what's queued is dropped
// for a fresh snapshot.

var (
	// eventQueue is how many events may wait for a client.
	eventQueue = 256
	// eventWriteTimeout is how long a client may take nothing before the
	// stream ends.
	eventWriteTimeout = 30 * time.Second
)

// follower is one client's events.
type follower struct {
	seen       view
	lastNotice uint64
	out        chan api.Event
}

func (a *API) events(ctx context.Context, _ *wire.Call, s *wire.Stream) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, next := a.b.Changes()
	st, err := a.b.State(ctx)
	if err != nil {
		return err
	}
	f := &follower{out: make(chan api.Event, eventQueue)}
	f.out <- f.snapshot(st)

	done := make(chan struct{})
	go func() { // the client goes: its end closes
		defer cancel()
		_, _ = s.Read(make([]byte, 1))
	}()
	go func() {
		defer close(done)
		defer cancel()
		for {
			select {
			case ev := <-f.out:
				_ = s.SetWriteDeadline(time.Now().Add(eventWriteTimeout))
				if err := s.Send(ev); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	for {
		select {
		case <-next:
		case <-ctx.Done():
			return nil
		}
		_, next = a.b.Changes()
		st, err := a.b.State(ctx)
		if err != nil {
			continue // the next change tries again
		}
		f.update(st)
	}
}

// snapshot is everything the client may see, from now on what it has seen.
func (f *follower) snapshot(st State) api.Event {
	f.seen = newView(st)
	for _, n := range st.Notices {
		f.lastNotice = max(f.lastNotice, n.Seq)
	}
	return api.Event{Type: api.EventSnapshot, Sessions: f.seen.list(), Inbox: f.seen.allInbox()}
}

// update queues what changed since the client last saw st, or a snapshot
// when its queue is full.
func (f *follower) update(st State) {
	for _, ev := range f.diff(st) {
		select {
		case f.out <- ev:
			continue
		default:
		}
		for len(f.out) > 0 { // drop what's queued: the snapshot replaces it
			select {
			case <-f.out:
			default:
			}
		}
		f.out <- f.snapshot(st)
		return
	}
}

// diff is what changed since the client last saw the state, and makes st what
// it has seen.
func (f *follower) diff(st State) []api.Event {
	now := newView(st)
	var evs []api.Event
	for _, id := range sortedKeys(f.seen.sessions) {
		if _, ok := now.sessions[id]; !ok {
			evs = append(evs, api.Event{Type: api.EventRemoved, ID: id})
		}
	}
	for _, s := range now.list() {
		old, ok := f.seen.sessions[s.ID]
		switch {
		case !ok:
			evs = append(evs, api.Event{Type: api.EventAdded, Session: &s})
		case old != s:
			evs = append(evs, api.Event{Type: api.EventChanged, Session: &s})
		}
	}
	for _, id := range sortedKeys(now.sessions) {
		items := now.inbox[id]
		if old := f.seen.inbox[id]; !reflect.DeepEqual(old, items) {
			evs = append(evs, api.Event{Type: api.EventInbox, ID: id, Inbox: items})
		}
	}
	for _, n := range st.Notices {
		if n.Seq <= f.lastNotice {
			continue
		}
		f.lastNotice = n.Seq
		if _, ok := now.sessions[n.SessionID]; ok {
			no := notice(n)
			evs = append(evs, api.Event{Type: api.EventNotice, Notice: &no})
		}
	}
	f.seen = now
	return evs
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
