package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Clients see what contains a session as its parent: its set if it's in
// one, else its group (section 7 of the design).
func TestSessionJSONNamesItsParent(t *testing.T) {
	enc := func(s Session) map[string]any {
		t.Helper()
		b, err := json.Marshal(&s)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "SetID") || strings.Contains(string(b), "SetOrder") {
			t.Errorf("the store's set fields went on the wire: %s", b)
		}
		return m
	}
	alone := enc(Session{ID: "a", GroupID: "g"})
	if alone["parent"] != "g" || alone["order"] != nil {
		t.Errorf("a session on its own: parent %v, order %v; want g, none", alone["parent"], alone["order"])
	}
	inSet := enc(Session{ID: "b", GroupID: "g", SetID: "s", SetOrder: 2})
	if inSet["parent"] != "s" || inSet["order"] != float64(2) || inSet["GroupID"] != "g" {
		t.Errorf("a session in a set: %v", inSet)
	}
	// Stale values a client sent back are replaced, never trusted.
	stale := enc(Session{ID: "c", GroupID: "g", Parent: "old", Order: 9})
	if stale["parent"] != "g" || stale["order"] != nil {
		t.Errorf("stale parent/order survived: %v", stale)
	}
}

func TestSessionJSONSaysWhyItNeedsYou(t *testing.T) {
	since := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	b, err := json.Marshal(&Session{ID: "a", Status: StatusWaiting, StatusSince: since})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"attention":{"rank":1,"reason":"waiting","since":"2026-11-20T09:00:00Z"}`) {
		t.Errorf("waiting session: %s", b)
	}
	b, _ = json.Marshal(&Session{ID: "b", Status: StatusBusy})
	if strings.Contains(string(b), "attention") || strings.Contains(string(b), "StatusSince") {
		t.Errorf("a busy session needs nothing: %s", b)
	}
}

// Groups are entities: they say their type, and the order you arranged them
// in. Collapse is a client's view, so it isn't sent with the group.
func TestGroupJSON(t *testing.T) {
	b, err := json.Marshal(&Group{ID: "g", Name: "api", SortKey: 20, Collapsed: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "group" || m["order"] != float64(20) || m["Name"] != "api" {
		t.Errorf("group: %s", b)
	}
	if _, ok := m["Collapsed"]; ok {
		t.Errorf("collapse went with the group: %s", b)
	}
	b, _ = json.Marshal(Group{ID: "default", IsDefault: true, SortKey: 5})
	if strings.Contains(string(b), `"order"`) {
		t.Errorf("Default has no order, it's always last: %s", b)
	}
}

func TestViewCollapsesGroupsByID(t *testing.T) {
	groups := []*Group{{ID: "default", Name: "Default", IsDefault: true}, {ID: "g", Name: "G", SortKey: 1}}
	ss := []*Session{{ID: "a", GroupID: "g"}, {ID: "b", GroupID: "default"}}
	rows := Layout(ss, groups, ViewState{Mode: "groups", Collapsed: []string{"g"}})
	if !rows[0].Collapsed || rows[1].Group.ID != "default" || rows[2].Session.ID != "b" || rows[2].Number != 1 {
		t.Errorf("collapsed by the view: %+v", rows)
	}
}
