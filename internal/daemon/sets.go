package daemon

import (
	"context"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// wireSet is a set as clients see it.
func wireSet(s *core.Set) ipc.Set {
	return ipc.Set{Type: ipc.EntitySet, ID: s.ID, Parent: s.GroupID, Host: s.Host}
}

func wireSets(sets []*core.Set) []ipc.Set {
	out := make([]ipc.Set, 0, len(sets))
	for _, s := range sets {
		out = append(out, wireSet(s))
	}
	return out
}

// mutateSet applies a set.* mutation. d.mu is held.
func (d *Daemon) mutateSet(ctx context.Context, st *store.Store, m ipc.Mutation) (any, error) {
	switch m.Kind {
	case ipc.MutSetCreate:
		s, err := st.CreateSet(ctx, m.SetID, m.IDs...)
		if err != nil {
			return nil, err
		}
		return wireSet(s), nil
	case ipc.MutSetAdd:
		return nil, st.AddToSet(ctx, m.SetID, m.ID, m.After)
	case ipc.MutSetRemove:
		return nil, st.RemoveFromSet(ctx, m.ID)
	default: // ipc.MutSetEnd
		return nil, st.EndSet(ctx, m.SetID)
	}
}

func isSetMutation(kind string) bool {
	switch kind {
	case ipc.MutSetCreate, ipc.MutSetAdd, ipc.MutSetRemove, ipc.MutSetEnd:
		return true
	}
	return false
}

// joinSet puts a session just made by a split in a set with the one it was
// split from: that session's set, right after it, or a new set of the two.
// The session already runs, so a failure is logged, not returned: it shows
// on its own. It returns the session as it is now.
func (d *Daemon) joinSet(ctx context.Context, s *core.Session, with string) *core.Session {
	if with == "" {
		return s
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err := func() error {
		target, err := d.store.Session(ctx, with)
		if err != nil {
			return err
		}
		if target.SetID != "" {
			return d.store.AddToSet(ctx, target.SetID, s.ID, target.ID)
		}
		_, err = d.store.CreateSet(ctx, "", target.ID, s.ID)
		return err
	}()
	if err != nil {
		d.cfg.Log.Printf("split: %s stays on its own, not with %s: %v", s.ID, with, err)
		return s
	}
	d.changes.bump()
	if now, err := d.store.Session(ctx, s.ID); err == nil {
		return now
	}
	return s
}

// layoutKeyFor is where a client's pane layout is kept.
func layoutKeyFor(clientID string) string {
	if clientID == "" {
		return layoutKey
	}
	return layoutKey + "/" + clientID
}
