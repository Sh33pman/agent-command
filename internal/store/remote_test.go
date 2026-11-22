package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
)

func TestAuthorizedClients(t *testing.T) {
	s, _ := open(t)
	must(t, s.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "laptop", Fingerprint: "SHA256:a"}))
	must(t, s.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "phone", Fingerprint: "SHA256:b"}))
	// The same fingerprint again renames it, in its place.
	must(t, s.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "work laptop", Fingerprint: "SHA256:a"}))
	got, err := s.AuthorizedClients(ctx)
	must(t, err)
	want := []config.AuthorizedClient{{Name: "work laptop", Fingerprint: "SHA256:a"}, {Name: "phone", Fingerprint: "SHA256:b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if ok, err := s.RemoveAuthorizedClient(ctx, "SHA256:a"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := s.RemoveAuthorizedClient(ctx, "SHA256:a"); ok || err != nil {
		t.Fatal("removed twice", ok, err)
	}
	got, _ = s.AuthorizedClients(ctx)
	if len(got) != 1 || got[0].Name != "phone" {
		t.Fatalf("got %+v", got)
	}
}

func TestKnownHosts(t *testing.T) {
	s, _ := open(t)
	studio := config.KnownHost{Name: "studio", Address: "10.0.0.2:7433", Fingerprint: "SHA256:s"}
	mini := config.KnownHost{Name: "mini", Address: "10.0.0.3:7433", Fingerprint: "SHA256:m", Cert: "/c.pem"}
	must(t, s.SaveKnownHost(ctx, "", studio))
	must(t, s.SaveKnownHost(ctx, "", mini))
	if err := s.SaveKnownHost(ctx, "", studio); err == nil {
		t.Fatal("added a name twice")
	}
	// A rename keeps its place.
	renamed := studio
	renamed.Name, renamed.Address = "studio-mac", "10.0.0.9:7433"
	must(t, s.SaveKnownHost(ctx, "studio", renamed))
	got, err := s.KnownHosts(ctx)
	must(t, err)
	if !reflect.DeepEqual(got, []config.KnownHost{renamed, mini}) {
		t.Fatalf("got %+v", got)
	}
	if err := s.SaveKnownHost(ctx, "gone", mini); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacing a missing host: %v", err)
	}
	if ok, err := s.RemoveKnownHost(ctx, "mini"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.RemoveKnownHost(ctx, "mini"); ok {
		t.Fatal("removed twice")
	}
}

func TestClientScopes(t *testing.T) {
	s, p := open(t)
	must(t, s.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "old", Fingerprint: "SHA256:o"}))
	// Back to before scopes, and up again: clients from then keep admin.
	for _, q := range []string{`ALTER TABLE remote_clients DROP COLUMN scope`, `PRAGMA user_version = 16`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	must(t, s.Close())
	s2, err := Open(ctx, p)
	must(t, err)
	defer s2.Close()
	must(t, s2.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "new", Fingerprint: "SHA256:n"}))
	got, err := s2.AuthorizedClients(ctx)
	must(t, err)
	if len(got) != 2 || got[0].ScopeOf() != config.ScopeAdmin || got[1].ScopeOf() != config.ScopeAct {
		t.Fatalf("scopes = %+v", got)
	}
	must(t, s2.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "new", Fingerprint: "SHA256:n", Scope: config.ScopeRead}))
	must(t, s2.AddAuthorizedClient(ctx, config.AuthorizedClient{Name: "renamed", Fingerprint: "SHA256:n"})) // a rename keeps it
	got, _ = s2.AuthorizedClients(ctx)
	if got[1].Name != "renamed" || got[1].Scope != config.ScopeRead {
		t.Errorf("after a rename: %+v", got[1])
	}
}
