package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// The event stream (design 8.3). On each change the daemon builds its
// snapshot, compares it with the last one entity by entity, and sends what
// was added, updated (whole) or removed, each with a revision, to every
// subscriber. A short log lets a client that reconnects get only what it
// missed. A subscriber that can't keep up never holds up the daemon: it's
// told to resync, and gets a new snapshot.

var (
	// eventLog is how many events are kept for clients that reconnect.
	eventLog = 1000
	// eventBuffer is how many events may wait for one subscriber.
	eventBuffer = 256
	// eventKeep is how long after the last subscriber goes the daemon keeps
	// following changes, so one that drops and reconnects can resume.
	eventKeep = 2 * time.Minute
	// eventSettle lets changes made together go out together.
	eventSettle = 10 * time.Millisecond
	// eventWriteTimeout is how long a subscriber may take to read an event.
	eventWriteTimeout = 30 * time.Second
)

// entityKey names an entity: its type, and its id on its host (ids are
// unique only within their host, design 7.4).
type entityKey struct{ typ, host, id string }

type entity struct {
	key  entityKey
	data []byte
}

// entitiesOf is a snapshot as entities: each list's by id, then the single
// values. Dismissed is each client's, so it isn't here.
func entitiesOf(s ipc.Snapshot) ([]entity, error) {
	var out []entity
	var err error
	addOn := func(typ, host, id string, v any) {
		if err != nil {
			return
		}
		var b []byte
		if b, err = json.Marshal(onTheWire(v)); err == nil {
			out = append(out, entity{entityKey{typ, host, id}, b})
		}
	}
	add := func(typ, id string, v any) { addOn(typ, "", id, v) }
	for _, x := range s.Sessions {
		addOn(ipc.EntitySession, x.Host, x.ID, x)
	}
	for _, x := range s.Groups {
		addOn(ipc.EntityGroup, x.Host, x.ID, x)
	}
	for _, x := range s.Sets {
		addOn(ipc.EntitySet, x.Host, x.ID, x)
	}
	for _, x := range s.Inbox {
		addOn(ipc.EntityInbox, x.Host, strconv.FormatInt(x.ID, 10), x)
	}
	for _, x := range s.Jobs {
		add(ipc.EntityJob, x.ID, x)
	}
	for _, x := range s.Notices {
		add(ipc.EntityNotice, strconv.FormatUint(x.Seq, 10), x)
	}
	// An entity of a type with no list of its own goes by its own type: a
	// client that doesn't know the type keeps it, for its children.
	for _, raw := range s.Others {
		var x struct{ Type, ID, Host string }
		if json.Unmarshal(raw, &x) == nil && x.Type != "" && x.ID != "" {
			addOn(x.Type, x.Host, x.ID, raw)
		}
	}
	for _, x := range s.RemoteHosts {
		add(ipc.EntityRemoteHost, x.Name, x)
	}
	add(ipc.EntityView, "", s.ViewState)
	add(ipc.EntitySettings, "", s.Settings)
	add(ipc.EntityHealth, "", s.Health)
	add(ipc.EntityFeatures, "", s.Features)
	add(ipc.EntityUpdate, "", s.Update)
	add(ipc.EntitySpeech, "", s.Speech)
	add(ipc.EntityRemote, "", s.Remote)
	add(ipc.EntityWake, "", s.Wake)
	return out, err
}

// only keeps what a subscriber wants of a snapshot.
func only(s ipc.Snapshot, wants func(string) bool) ipc.Snapshot {
	for typ, clear := range map[string]func(){
		ipc.EntitySession: func() { s.Sessions = nil }, ipc.EntityGroup: func() { s.Groups = nil },
		ipc.EntitySet: func() { s.Sets = nil }, ipc.EntityInbox: func() { s.Inbox = nil }, ipc.EntityJob: func() { s.Jobs = nil },
		ipc.EntityNotice: func() { s.Notices = nil }, ipc.EntityRemoteHost: func() { s.RemoteHosts = nil },
		ipc.EntityView: func() { s.ViewState = ipc.ViewState{} }, ipc.EntitySettings: func() { s.Settings = nil },
		ipc.EntityHealth: func() { s.Health = nil }, ipc.EntityDismissed: func() { s.Dismissed = nil },
		ipc.EntityFeatures: func() { s.Features = nil }, ipc.EntityUpdate: func() { s.Update = nil },
		ipc.EntitySpeech: func() { s.Speech = nil }, ipc.EntityRemote: func() { s.Remote = nil }, ipc.EntityWake: func() { s.Wake = nil },
	} {
		if !wants(typ) {
			clear()
		}
	}
	return s
}

type subscriber struct {
	types     []string // none: everything
	clientID  string
	ch        chan ipc.Event
	behind    atomic.Bool
	dismissed string // what it was last sent
}

func (s *subscriber) wants(typ string) bool {
	return len(s.types) == 0 || slices.Contains(s.types, typ)
}

// send queues ev, or marks the subscriber behind when its buffer is full.
func (s *subscriber) send(ev ipc.Event) {
	if s.behind.Load() || !s.wants(ev.Entity) {
		return
	}
	select {
	case s.ch <- ev:
	default:
		s.behind.Store(true)
	}
}

type eventStream struct {
	building sync.Mutex
	mu       sync.Mutex
	rev      uint64
	base     uint64 // the revision before the log's first event
	log      []ipc.Event
	last     map[entityKey][]byte
	snap     ipc.Snapshot // the last one built
	built    bool
	subs     map[*subscriber]bool
	idle     time.Time // when the last subscriber went
	refresh  chan struct{}
}

func newEventStream() *eventStream {
	return &eventStream{subs: map[*subscriber]bool{}, refresh: make(chan struct{}, 1)}
}

// publish compares snap with the last snapshot, and sends the difference.
func (e *eventStream) publish(snap ipc.Snapshot) error {
	ents, err := entitiesOf(snap)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	next := make(map[entityKey][]byte, len(ents))
	var evs []ipc.Event
	seen := map[entityKey]bool{}
	for _, en := range ents {
		next[en.key] = en.data
		seen[en.key] = true
		old, had := e.last[en.key]
		switch {
		case !had && e.built:
			evs = append(evs, ipc.Event{Type: ipc.EventAdded, Entity: en.key.typ, Host: en.key.host, ID: en.key.id, Data: en.data})
		case had && string(old) != string(en.data):
			evs = append(evs, ipc.Event{Type: ipc.EventUpdated, Entity: en.key.typ, Host: en.key.host, ID: en.key.id, Data: en.data})
		}
	}
	for k := range e.last {
		if !seen[k] {
			evs = append(evs, ipc.Event{Type: ipc.EventRemoved, Entity: k.typ, Host: k.host, ID: k.id})
		}
	}
	e.last, e.snap = next, snap
	if !e.built {
		e.built = true
		e.base = e.rev
		return nil
	}
	for _, ev := range evs {
		e.rev++
		ev.Rev = e.rev
		e.log = append(e.log, ev)
		for s := range e.subs {
			s.send(ev)
		}
	}
	if over := len(e.log) - eventLog; over > 0 {
		e.base = e.log[over-1].Rev
		e.log = slices.Delete(e.log, 0, over)
	}
	return nil
}

// forgetIfIdle drops what the stream knows when no one has followed it for
// a while, and says whether it did: a client that comes back gets a new
// snapshot. It's one step, so a client subscribing meanwhile isn't missed.
func (e *eventStream) forgetIfIdle(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activeLocked(now) {
		return false
	}
	if e.built {
		e.rev++
		e.base, e.log, e.last, e.built = e.rev, nil, nil, false
	}
	return true
}

// active says whether anyone follows the stream, or did a moment ago.
func (e *eventStream) active(now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.activeLocked(now)
}

func (e *eventStream) activeLocked(now time.Time) bool {
	return len(e.subs) > 0 || !e.idle.IsZero() && now.Sub(e.idle) < eventKeep
}

// followEvents builds the stream from the daemon's changes while anyone
// follows it.
func (d *Daemon) followEvents(ctx context.Context) {
	var seen uint64 // the revision the last snapshot was built after
	for {
		rev, next := d.changes.current()
		if rev == seen {
			select {
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			case <-next:
			case <-d.events.refresh:
			}
		}
		time.Sleep(eventSettle)
		// The revision before the snapshot is built: a change made while it's
		// built moves it on, and the loop goes round again for it.
		seen, _ = d.changes.current()
		if d.events.forgetIfIdle(d.cfg.Now()) {
			continue
		}
		if err := d.publishEvents(ctx); err != nil {
			d.cfg.Log.Printf("events: %v", err)
		}
	}
}

// publishEvents builds the snapshot and sends what changed, and each
// subscriber its own dismissed warnings when they changed.
func (d *Daemon) publishEvents(ctx context.Context) error {
	d.events.building.Lock() // one at a time, so an older snapshot never follows a newer one
	defer d.events.building.Unlock()
	snap, err := d.snapshot(ctx, "")
	if err != nil {
		return err
	}
	if err := d.events.publish(snap); err != nil {
		return err
	}
	e := d.events
	e.mu.Lock()
	subs := make([]*subscriber, 0, len(e.subs))
	for s := range e.subs {
		subs = append(subs, s)
	}
	rev := e.rev
	e.mu.Unlock()
	for _, s := range subs {
		if s.clientID == "" || !s.wants(ipc.EntityDismissed) {
			continue
		}
		ids, err := d.dismissed(ctx, s.clientID)
		if err != nil {
			continue
		}
		b, _ := json.Marshal(ids)
		if string(b) != s.dismissed {
			s.dismissed = string(b)
			s.send(ipc.Event{Rev: rev, Type: ipc.EventUpdated, Entity: ipc.EntityDismissed, Data: b})
		}
	}
	return nil
}

// subscribe adds a subscriber, and returns what it gets first: what it
// missed since req.Since, or a snapshot.
func (d *Daemon) subscribe(ctx context.Context, req ipc.SubscribeRequest) (*subscriber, []ipc.Event, error) {
	for _, t := range req.Types {
		if !slices.Contains(ipc.EntityTypes, t) {
			return nil, nil, invalid("no entity type %q: they're %v", t, ipc.EntityTypes)
		}
	}
	s := &subscriber{types: req.Types, clientID: callerOf(ctx).clientID, ch: make(chan ipc.Event, eventBuffer)}
	dismissed, err := d.dismissed(ctx, s.clientID)
	if err != nil {
		return nil, nil, err
	}
	b, _ := json.Marshal(dismissed)
	s.dismissed = string(b)
	// Registered first, so the stream isn't forgotten while it's built; then
	// built if no one followed it; then what it gets first, which covers
	// everything queued for it meanwhile.
	e := d.events
	e.mu.Lock()
	e.subs[s] = true
	built := e.built
	e.mu.Unlock()
	if !built {
		if err := d.publishEvents(ctx); err != nil {
			d.unsubscribe(s)
			return nil, nil, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(s.ch) > 0 {
		<-s.ch
	}
	s.behind.Store(false)
	if req.Since > 0 && req.Since >= e.base && req.Since <= e.rev {
		var missed []ipc.Event
		for _, ev := range e.log {
			if ev.Rev > req.Since && s.wants(ev.Entity) {
				missed = append(missed, ev)
			}
		}
		return s, missed, nil
	}
	return s, []ipc.Event{e.snapshotFor(s, dismissed)}, nil
}

// snapshotFor is the snapshot event for a subscriber (e.mu is held).
func (e *eventStream) snapshotFor(s *subscriber, dismissed []string) ipc.Event {
	snap := e.snap
	snap.Dismissed = dismissed
	snap = only(snap, s.wants)
	return ipc.Event{Rev: e.rev, Type: ipc.EventSnapshot, Snapshot: &snap}
}

func (d *Daemon) unsubscribe(s *subscriber) {
	e := d.events
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.subs, s)
	if len(e.subs) == 0 {
		e.idle = d.cfg.Now()
	}
}

// resync starts a subscriber that fell behind again from a new snapshot.
func (d *Daemon) resync(ctx context.Context, s *subscriber) []ipc.Event {
	dismissed, _ := d.dismissed(ctx, s.clientID)
	e := d.events
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(s.ch) > 0 {
		<-s.ch
	}
	s.behind.Store(false)
	return []ipc.Event{{Rev: e.rev, Type: ipc.EventResync}, e.snapshotFor(s, dismissed)}
}

// stream sends a subscriber its events until send fails or ctx ends.
func (d *Daemon) stream(ctx context.Context, req ipc.SubscribeRequest, send func(ipc.Event) error) error {
	s, first, err := d.subscribe(ctx, req)
	if err != nil {
		return err
	}
	defer d.unsubscribe(s)
	for _, ev := range first {
		if err := send(ev); err != nil {
			return nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-d.stop:
			return nil
		case ev := <-s.ch:
			evs := []ipc.Event{ev}
			if s.behind.Load() {
				evs = d.resync(ctx, s)
			}
			for _, ev := range evs {
				if err := send(ev); err != nil {
					return nil
				}
			}
		}
	}
}

// serveEvents turns a socket connection into a subscriber's stream: an OK
// reply, then one event per line until the client closes its end.
func (d *Daemon) serveEvents(ctx context.Context, c net.Conn, r *bufio.Reader, req ipc.Request) {
	var sr ipc.SubscribeRequest
	if len(req.Data) > 0 {
		var err error
		if sr, err = decode[ipc.SubscribeRequest](req.Data); err != nil {
			_ = ipc.WriteJSON(c, ipc.Response{Error: err.Error(), Code: ipc.CodeInvalid})
			return
		}
	}
	for _, t := range sr.Types {
		if !slices.Contains(ipc.EntityTypes, t) {
			_ = ipc.WriteJSON(c, ipc.Response{Error: "no entity type " + strconv.Quote(t), Code: ipc.CodeInvalid})
			return
		}
	}
	if err := ipc.WriteJSON(c, ipc.Response{OK: true}); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // the client closes its end
		defer cancel()
		_, _ = r.ReadByte()
	}()
	_ = d.stream(ctx, sr, func(ev ipc.Event) error {
		_ = c.SetWriteDeadline(time.Now().Add(eventWriteTimeout))
		return ipc.WriteJSON(c, ev)
	})
}
