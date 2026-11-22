package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
)

// subscribed is a stream, read in the background.
type subscribed struct {
	t   *testing.T
	c   *ipc.Conn
	evs chan ipc.Event
}

func subscribeTo(t *testing.T, p paths.Paths, req ipc.SubscribeRequest) *subscribed {
	t.Helper()
	c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: testVersion,
		Client: &ipc.ClientHello{ID: "mac-1", Kind: ipc.KindMac}})
	must(t, err)
	t.Cleanup(func() { c.Close() })
	must(t, c.Subscribe(req))
	s := &subscribed{t: t, c: c, evs: make(chan ipc.Event, 10000)}
	go func() {
		for {
			ev, err := c.NextEvent()
			if err != nil {
				close(s.evs)
				return
			}
			s.evs <- ev
		}
	}()
	return s
}

// next is the next event, or fails after a while.
func (s *subscribed) next() ipc.Event {
	s.t.Helper()
	select {
	case ev, ok := <-s.evs:
		if !ok {
			s.t.Fatal("the stream ended")
		}
		return ev
	case <-time.After(5 * time.Second):
		s.t.Fatal("no event")
	}
	return ipc.Event{}
}

// until reads events until one matches.
func (s *subscribed) until(what string, match func(ipc.Event) bool) ipc.Event {
	s.t.Helper()
	for {
		if ev := s.next(); match(ev) {
			return ev
		} else if ev.Type == "" {
			s.t.Fatalf("waiting for %s", what)
		}
	}
}

func is(typ, entity string) func(ipc.Event) bool {
	return func(ev ipc.Event) bool { return ev.Type == typ && ev.Entity == entity }
}

// A subscriber gets a snapshot, then each entity added, updated or removed.
func TestTheStreamSendsEntities(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	s := subscribeTo(t, p, ipc.SubscribeRequest{})
	first := s.next()
	if first.Type != ipc.EventSnapshot || first.Snapshot == nil || len(first.Snapshot.Groups) == 0 {
		t.Fatalf("first = %+v", first)
	}
	g := groupNamed(t, c, "Backend")
	added := s.until("the group", is(ipc.EventAdded, ipc.EntityGroup))
	var group ipc.Group
	must(t, json.Unmarshal(added.Data, &group))
	if added.ID != g || group.Name != "Backend" || added.Rev <= first.Rev {
		t.Errorf("added = %+v %+v", added, group)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g, Text: "API"})
	updated := s.until("the rename", is(ipc.EventUpdated, ipc.EntityGroup))
	must(t, json.Unmarshal(updated.Data, &group))
	if group.Name != "API" || updated.Rev <= added.Rev {
		t.Errorf("updated = %+v", group)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g})
	if removed := s.until("the delete", is(ipc.EventRemoved, ipc.EntityGroup)); removed.ID != g || len(removed.Data) != 0 {
		t.Errorf("removed = %+v", removed)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewSort, Text: "name"})
	view := s.until("the view", is(ipc.EventUpdated, ipc.EntityView))
	var vs ipc.ViewState
	must(t, json.Unmarshal(view.Data, &vs))
	if vs.Sort != "name" || view.ID != "" {
		t.Errorf("view = %+v", vs)
	}
	// A client's own dismissed warnings, to it alone.
	must(t, c.Call(ipc.OpHealthDismiss, ipc.HealthRef{ClientID: "mac-1", ID: "hooks"}, nil))
	if d := s.until("dismissed", is(ipc.EventUpdated, ipc.EntityDismissed)); string(d.Data) != `["hooks"]` {
		t.Errorf("dismissed = %s", d.Data)
	}
}

// A client that drops and reconnects gets only what it missed; one too far
// behind gets a new snapshot. A client can ask for some types only.
func TestTheStreamResumes(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	s := subscribeTo(t, p, ipc.SubscribeRequest{})
	s.next()
	g := groupNamed(t, c, "One")
	last := s.until("the group", is(ipc.EventAdded, ipc.EntityGroup)).Rev
	s.c.Close()

	groupNamed(t, c, "Two")
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g, Text: "Uno"})
	time.Sleep(100 * time.Millisecond) // the stream follows while a client may come back
	back := subscribeTo(t, p, ipc.SubscribeRequest{Since: last})
	first := back.next()
	if first.Type == ipc.EventSnapshot {
		t.Fatalf("resumed with a snapshot: %+v", first)
	}
	if first.Rev != last+1 || first.Entity != ipc.EntityGroup {
		t.Errorf("first missed = %+v", first)
	}
	// What it missed, in whichever order they came together: Two added, One renamed.
	seen := map[string]bool{first.Type: true}
	for !seen[ipc.EventAdded] || !seen[ipc.EventUpdated] {
		ev := back.next()
		if ev.Entity == ipc.EntityGroup {
			seen[ev.Type] = true
		}
	}

	// Too far behind: a snapshot.
	if ev := subscribeTo(t, p, ipc.SubscribeRequest{Since: 1 << 40}).next(); ev.Type != ipc.EventSnapshot {
		t.Errorf("from the future: %+v", ev)
	}
	// Groups only: a snapshot of groups, then group events.
	only := subscribeTo(t, p, ipc.SubscribeRequest{Types: []string{ipc.EntityGroup}})
	snap := only.next()
	if snap.Snapshot == nil || len(snap.Snapshot.Groups) == 0 || snap.Snapshot.Settings != nil || snap.Snapshot.Health != nil {
		t.Errorf("groups only: %+v", snap.Snapshot)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewSort, Text: "created"})
	groupNamed(t, c, "Three")
	if ev := only.next(); ev.Entity != ipc.EntityGroup {
		t.Errorf("not a group: %+v", ev)
	}
	bad := dial(t, p)
	if err := bad.Subscribe(ipc.SubscribeRequest{Types: []string{"weather"}}); err == nil {
		t.Error("an entity type that isn't one")
	}
	bad2 := dial(t, p)
	if err := bad2.Call(ipc.OpSubscribe, "x", nil); err == nil {
		t.Error("a request that isn't one")
	}
}

// A client that can't keep up doesn't slow the others: publishing never
// waits for it; it's marked behind, told to resync, and gets a new snapshot.
func TestASlowClientResyncs(t *testing.T) {
	old := eventBuffer
	eventBuffer = 2
	t.Cleanup(func() { eventBuffer = old })
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	d.events = newEventStream()
	ctx := context.Background()
	groups := []*ipc.Group{}
	snap := func() ipc.Snapshot { return ipc.Snapshot{Groups: append([]*ipc.Group(nil), groups...)} }
	must(t, d.events.publish(snap()))
	d.events.built = true
	slow, _, err := d.subscribe(ctx, ipc.SubscribeRequest{})
	must(t, err)
	fast, _, err := d.subscribe(ctx, ipc.SubscribeRequest{Types: []string{ipc.EntityGroup}})
	must(t, err)
	got := 0
	for i := range 8 {
		groups = append(groups, &ipc.Group{ID: string(rune('a' + i)), Type: "group", Name: "G"})
		done := make(chan struct{})
		go func() { must(t, d.events.publish(snap())); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("publishing waited for the slow client")
		}
		<-fast.ch
		got++
	}
	if got != 8 || fast.behind.Load() {
		t.Errorf("the fast client got %d, behind %v", got, fast.behind.Load())
	}
	if !slow.behind.Load() {
		t.Fatal("the slow client isn't behind")
	}
	evs := d.resync(ctx, slow)
	if len(evs) != 2 || evs[0].Type != ipc.EventResync || evs[1].Type != ipc.EventSnapshot || len(evs[1].Snapshot.Groups) != 8 ||
		slow.behind.Load() || len(slow.ch) != 0 {
		t.Errorf("resync = %+v", evs)
	}
	// Behind, it gets nothing more until it resyncs.
	slow.behind.Store(true)
	slow.send(ipc.Event{Type: ipc.EventAdded, Entity: ipc.EntityGroup})
	if len(slow.ch) != 0 {
		t.Error("sent to a client that's behind")
	}
	d.unsubscribe(slow)
	d.unsubscribe(fast)
	if !d.events.active(d.cfg.Now()) {
		t.Error("the stream stops at once: a client couldn't come back")
	}
	d.events.idle = d.cfg.Now().Add(-eventKeep - time.Second)
	if d.events.active(d.cfg.Now()) {
		t.Error("still followed long after")
	}
	if !d.events.forgetIfIdle(d.cfg.Now()) || d.events.built || d.events.log != nil {
		t.Error("forgotten")
	}
}

// An entity of a type with no list of its own goes out by its own type, so a
// client that doesn't know the type keeps it, for its children (design 7.2).
func TestOtherEntitiesGoOutByTheirType(t *testing.T) {
	s := ipc.Snapshot{Others: []json.RawMessage{
		json.RawMessage(`{"type":"folder","id":"f1","parent":"g1"}`),
		json.RawMessage(`{"type":"folder","id":"f2","host":"studio","parent":"default"}`),
		json.RawMessage(`{"id":"no-type"}`), json.RawMessage(`not json`),
	}}
	ens, err := entitiesOf(s)
	if err != nil {
		t.Fatal(err)
	}
	var folders []entityKey
	for _, en := range ens {
		if en.key.typ == "folder" {
			folders = append(folders, en.key)
		}
	}
	if len(folders) != 2 || folders[0].id != "f1" || folders[1].host != "studio" {
		t.Errorf("folders = %+v", folders)
	}
}
