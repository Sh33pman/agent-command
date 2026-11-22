package client

import (
	"encoding/json"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// A host's tree follows its events one by one, not only snapshots.
func TestTreeFollowsEachEvent(t *testing.T) {
	tr := newTree()
	data := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		must(t, err)
		return b
	}
	tr.apply(ipc.Event{Type: ipc.EventSnapshot, Snapshot: &ipc.Snapshot{Sessions: []*ipc.Session{{ID: "a"}}}})
	steps := []ipc.Event{
		{Type: ipc.EventAdded, Entity: ipc.EntitySession, ID: "b", Data: data(ipc.Session{ID: "b", Name: "web"})},
		{Type: ipc.EventUpdated, Entity: ipc.EntitySession, ID: "a", Data: data(ipc.Session{ID: "a", Name: "api"})},
		{Type: ipc.EventAdded, Entity: ipc.EntityGroup, ID: "g", Data: data(ipc.Group{ID: "g", Name: "G"})},
		{Type: ipc.EventAdded, Entity: ipc.EntitySet, ID: "s", Data: data(ipc.Set{Type: ipc.EntitySet, ID: "s", Parent: "g"})},
		{Type: ipc.EventAdded, Entity: ipc.EntityInbox, ID: "7", Data: data(core.InboxItem{ID: 7, SessionID: "a", State: core.InboxOpen})},
		{Type: ipc.EventUpdated, Entity: ipc.EntityView, Data: data(ipc.ViewState{})}, // not followed
	}
	for _, ev := range steps {
		if n := tr.apply(ev); n != nil {
			t.Errorf("a notice from %+v", ev)
		}
	}
	if tr.sessions["a"].Name != "api" || tr.sessions["b"] == nil || tr.groups["g"] == nil || tr.sets["s"].Parent != "g" || len(tr.inbox) != 1 {
		t.Fatalf("tree = %+v", tr)
	}
	if got := tr.order[ipc.EntitySession]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("order = %v", got)
	}
	n := tr.apply(ipc.Event{Type: ipc.EventAdded, Entity: ipc.EntityNotice, ID: "3", Data: data(ipc.Notice{Seq: 3, Kind: "done"})})
	if n == nil || n.Seq != 3 {
		t.Errorf("notice = %+v", n)
	}
	if tr.apply(ipc.Event{Type: ipc.EventUpdated, Entity: ipc.EntityNotice, ID: "3", Data: data(ipc.Notice{Seq: 3})}) != nil {
		t.Error("an update to a notice is no new notice")
	}
	for _, ev := range []ipc.Event{
		{Type: ipc.EventRemoved, Entity: ipc.EntitySession, ID: "b"},
		{Type: ipc.EventRemoved, Entity: ipc.EntityGroup, ID: "g"},
		{Type: ipc.EventRemoved, Entity: ipc.EntitySet, ID: "s"},
		{Type: ipc.EventRemoved, Entity: ipc.EntityInbox, ID: "7"},
		{Type: ipc.EventUpdated, Entity: ipc.EntitySession, ID: "a", Data: json.RawMessage(`"not a session"`)},
	} {
		tr.apply(ev)
	}
	if len(tr.sessions) != 0 || len(tr.groups) != 0 || len(tr.sets) != 0 || len(tr.inbox) != 0 || len(tr.order[ipc.EntitySession]) != 0 {
		t.Errorf("after the removals: %+v", tr)
	}
}
