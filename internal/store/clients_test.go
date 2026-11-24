package store

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestClients(t *testing.T) {
	s, _ := open(t)
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	must(t, s.SaveClient(ctx, Client{ID: "mac", Kind: "mac", Device: "studio", Caps: []string{"notify", "sound"}, OnHost: true}))
	must(t, s.SetPresence(ctx, "mac", "active", "s1", true))
	clock = clock.Add(time.Hour)
	must(t, s.SaveClient(ctx, Client{ID: "terminal", Kind: "terminal"}))
	// Saving again keeps its presence and when it was active.
	must(t, s.SaveClient(ctx, Client{ID: "mac", Kind: "mac", Device: "studio 2", Caps: []string{"notify"}}))
	must(t, s.SetPresence(ctx, "terminal", "idle", "", false))
	got, err := s.Clients(ctx)
	must(t, err)
	if len(got) != 2 || got[0].ID != "mac" && got[0].ID != "terminal" {
		t.Fatalf("clients = %+v", got)
	}
	byID := map[string]Client{}
	for _, c := range got {
		byID[c.ID] = c
	}
	mac := byID["mac"]
	if mac.Device != "studio 2" || !slices.Equal(mac.Caps, []string{"notify"}) || mac.OnHost || mac.Presence != "active" || mac.Focus != "s1" ||
		mac.LastActive.IsZero() || !mac.FirstSeen.Before(mac.LastSeen) {
		t.Errorf("mac = %+v", mac)
	}
	if term := byID["terminal"]; term.Presence != "idle" || !term.LastActive.IsZero() || len(term.Caps) != 0 {
		t.Errorf("terminal = %+v", term)
	}
	if err := s.SetPresence(ctx, "nope", "active", "", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("presence for no one: %v", err)
	}
	clock = clock.Add(48 * time.Hour)
	must(t, s.TouchClient(ctx, "terminal"))
	ids, err := s.ForgetClients(ctx, clock.Add(-24*time.Hour))
	if err != nil || !slices.Equal(ids, []string{"mac"}) {
		t.Errorf("forgot %v %v", ids, err)
	}
	if got, _ := s.Clients(ctx); len(got) != 1 || got[0].ID != "terminal" {
		t.Errorf("left = %+v", got)
	}
}

// Each client's dismissed warnings move from where 1.3 kept them into the
// registry, with the client.
func TestDismissedWarningsMoveToTheRegistry(t *testing.T) {
	s, p := open(t)
	for _, q := range []string{
		`ALTER TABLE clients DROP COLUMN profile`, `ALTER TABLE clients DROP COLUMN dismissed`, `ALTER TABLE clients DROP COLUMN keymap`, `ALTER TABLE events DROP COLUMN caller`, `ALTER TABLE remote_clients DROP COLUMN scope`, `DROP TABLE requests`,
		`INSERT INTO app_state (key, value) VALUES ('health.dismissed:mac-1', '["hooks"]'), ('health.dismissed:old', '')`,
		`DROP TABLE harnesses`, `ALTER TABLE sessions DROP COLUMN harness_id`, `ALTER TABLE sessions RENAME COLUMN harness_kind TO harness`, `ALTER TABLE sessions ADD COLUMN bg_id TEXT NOT NULL DEFAULT ''`, `ALTER TABLE sessions ADD COLUMN external TEXT NOT NULL DEFAULT ''`, `PRAGMA user_version = 14`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	must(t, s.Close())
	s2, err := Open(ctx, p)
	must(t, err)
	defer s2.Close()
	if v, err := s2.ClientField(ctx, "mac-1", ClientDismissed); err != nil || v != `["hooks"]` {
		t.Errorf("dismissed = %q %v", v, err)
	}
	if v, _ := s2.AppState(ctx, "health.dismissed:mac-1"); v != "" {
		t.Errorf("left behind: %q", v)
	}
	cs, _ := s2.Clients(ctx)
	if len(cs) != 1 || cs[0].LastSeen.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("clients = %+v: seen now, so not forgotten straight away", cs)
	}
}

func TestClientFields(t *testing.T) {
	s, _ := open(t)
	must(t, s.SetClientField(ctx, "new", ClientKeymap, `{"a":1}`)) // adds it
	must(t, s.SaveClient(ctx, Client{ID: "new", Kind: "mac"}))
	if v, err := s.ClientField(ctx, "new", ClientKeymap); err != nil || v != `{"a":1}` {
		t.Errorf("keymap = %q %v", v, err)
	}
	if v, err := s.ClientField(ctx, "nobody", ClientProfile); err != nil || v != "" {
		t.Errorf("no client: %q %v", v, err)
	}
	cs, vs, err := s.ClientsWith(ctx, ClientKeymap)
	if err != nil || len(cs) != 1 || cs[0].Kind != "mac" || vs[0] != `{"a":1}` {
		t.Errorf("with a keymap: %+v %v %v", cs, vs, err)
	}
	if _, err := s.ClientField(ctx, "new", "id; DROP TABLE clients"); err == nil {
		t.Error("any field")
	}
	if err := s.SetClientField(ctx, "new", "kind", "x"); err == nil {
		t.Error("set any field")
	}
	if _, _, err := s.ClientsWith(ctx, "kind"); err == nil {
		t.Error("list by any field")
	}
}
