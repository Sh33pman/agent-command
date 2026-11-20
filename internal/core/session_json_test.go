package core

import (
	"encoding/json"
	"strings"
	"testing"
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
