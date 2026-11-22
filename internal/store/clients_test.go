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
	must(t, s.SaveClient(ctx, Client{ID: "tui", Kind: "tui"}))
	// Saving again keeps its presence and when it was active.
	must(t, s.SaveClient(ctx, Client{ID: "mac", Kind: "mac", Device: "studio 2", Caps: []string{"notify"}}))
	must(t, s.SetPresence(ctx, "tui", "idle", "", false))
	got, err := s.Clients(ctx)
	must(t, err)
	if len(got) != 2 || got[0].ID != "mac" && got[0].ID != "tui" {
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
	if tui := byID["tui"]; tui.Presence != "idle" || !tui.LastActive.IsZero() || len(tui.Caps) != 0 {
		t.Errorf("tui = %+v", tui)
	}
	if err := s.SetPresence(ctx, "nope", "active", "", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("presence for no one: %v", err)
	}
	clock = clock.Add(48 * time.Hour)
	must(t, s.TouchClient(ctx, "tui"))
	ids, err := s.ForgetClients(ctx, clock.Add(-24*time.Hour))
	if err != nil || !slices.Equal(ids, []string{"mac"}) {
		t.Errorf("forgot %v %v", ids, err)
	}
	if got, _ := s.Clients(ctx); len(got) != 1 || got[0].ID != "tui" {
		t.Errorf("left = %+v", got)
	}
}
