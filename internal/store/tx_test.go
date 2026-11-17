package store

import (
	"errors"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
)

// What fn wrote is kept when it returns nil, and none of it when it fails.
func TestWithTxCommitsOrRollsBackTogether(t *testing.T) {
	s, _ := open(t)
	sess := &core.Session{Name: "first"}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err := s.WithTx(ctx, func(tx *Store) error {
		sess.Name = "changed"
		if err := tx.UpdateSession(ctx, sess); err != nil {
			return err
		}
		if err := tx.AddInboxItem(ctx, core.InboxItem{SessionID: sess.ID, Kind: core.InboxPermission, Text: "ok?"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithTx = %v", err)
	}
	if got, _ := s.Session(ctx, sess.ID); got.Name != "first" {
		t.Errorf("a failed transaction kept the session's change: %q", got.Name)
	}
	if items, _ := s.InboxQueue(ctx); len(items) != 0 {
		t.Errorf("a failed transaction kept an Inbox item: %+v", items)
	}

	err = s.WithTx(ctx, func(tx *Store) error {
		sess.Name = "changed"
		if err := tx.UpdateSession(ctx, sess); err != nil {
			return err
		}
		return tx.AppendEvent(ctx, LoggedEvent{SessionID: sess.ID, Kind: "test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Session(ctx, sess.ID); got.Name != "changed" {
		t.Errorf("a committed transaction lost the change: %q", got.Name)
	}
}

// A transaction inside a transaction joins it, rather than waiting for the one
// connection the outer one holds.
func TestWithTxNests(t *testing.T) {
	s, _ := open(t)
	err := s.WithTx(ctx, func(tx *Store) error {
		return tx.WithTx(ctx, func(inner *Store) error {
			return inner.CreateSession(ctx, &core.Session{Name: "nested"})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Sessions(ctx, false); len(all) != 1 {
		t.Errorf("sessions = %d, want 1", len(all))
	}
}
