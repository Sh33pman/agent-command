package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/store"
)

// Changes to many sessions at once: each id gets its own result. Every
// local id that can change does; one that can't says why, and the rest go
// on. Ids on a Remote host are tried one by one; once its host can't be
// reached, its other ids report that at once.

// batchKinds take IDs and return ipc.Results.
var batchKinds = []string{ipc.MutRename, ipc.MutArchive, ipc.MutUnarchive, ipc.MutRemote,
	ipc.MutMove, ipc.MutPurge}

func isBatch(m ipc.Mutation) bool {
	return len(m.IDs) > 0 && slices.Contains(batchKinds, m.Kind)
}

// codedError is an error with the code a Result carries.
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func invalid(format string, a ...any) error {
	return &codedError{code: ipc.CodeInvalid, err: fmt.Errorf(format, a...)}
}

// badRequest is a request that doesn't make sense: its arguments don't
// decode, or are missing.
func badRequest(format string, a ...any) error {
	return &codedError{code: ipc.CodeBadRequest, err: fmt.Errorf(format, a...)}
}

// errCoder is an error that knows its own code (a catalog.ProtoError).
type errCoder interface{ Code() string }

// unreachable marks an error from a host that couldn't be reached.
func unreachable(err error) error {
	if err == nil {
		return nil
	}
	var ce *wire.CallError
	if errors.As(err, &ce) {
		return err // the host answered: its own code
	}
	return &codedError{code: ipc.CodeHostUnreachable, err: err}
}

// resultOf is the Result for an id's error.
func resultOf(id string, err error) ipc.Result {
	if err == nil {
		return ipc.Result{ID: id, OK: true}
	}
	r := ipc.Result{ID: id, Code: ipc.CodeInternal, Error: err.Error()}
	var ce *codedError
	var we *wire.CallError
	var coder errCoder
	switch {
	case errors.As(err, &ce):
		r.Code = ce.code
	case errors.As(err, &coder) && coder.Code() != "":
		r.Code = coder.Code()
	case errors.Is(err, store.ErrNotFound):
		r.Code = ipc.CodeNotFound
	case errors.As(err, &we) && we.Code != "":
		r.Code = we.Code
	}
	return r
}

// mutateMany applies m to each of its ids.
func (d *Daemon) mutateMany(ctx context.Context, m ipc.Mutation) (ipc.Results, error) {
	var ids []string
	for _, id := range m.IDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var names []string
	if m.Kind == ipc.MutRename {
		if names = core.RenameNames(m.Text, len(ids)); names == nil {
			return ipc.Results{}, errors.New("name is empty")
		}
	}
	out := ipc.Results{Results: make([]ipc.Result, 0, len(ids))}
	down := map[string]error{} // hosts that couldn't be reached, and why
	for i, id := range ids {
		one := m
		one.IDs, one.ID = nil, id
		if names != nil {
			one.Text = names[i]
		}
		host := ""
		if s, err := d.sessionByID(ctx, id); err == nil {
			host = s.Host
		}
		var err error
		if why, ok := down[host]; ok && host != "" {
			err = why
		} else {
			err = d.mutateOne(ctx, one)
		}
		r := resultOf(id, err)
		if r.Code == ipc.CodeHostUnreachable && host != "" {
			down[host] = err
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// mutateOne is one id's change.
func (d *Daemon) mutateOne(ctx context.Context, m ipc.Mutation) error {
	switch m.Kind {
	case ipc.MutMove:
		d.mu.Lock()
		defer d.mu.Unlock()
		if _, err := d.store.Session(ctx, m.ID); err != nil {
			return err
		}
		return d.store.MoveSessions(ctx, m.GroupID, m.ID)
	case ipc.MutPurge:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.purge(ctx, m.ID)
	default:
		_, err := d.mutate(ctx, m)
		return err
	}
}

// purge deletes an archived session for good (d.mu is held).
func (d *Daemon) purge(ctx context.Context, id string) error {
	sess, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if sess.Status != core.StatusArchived {
		return invalid("%s isn't archived", nameOf(sess))
	}
	if sess.Kind == core.KindBackground && sess.BgID != "" && d.cfg.Remover != nil {
		if err := d.cfg.Remover.Remove(ctx, sess.BgID); err != nil {
			d.cfg.Log.Printf("purge: claude rm %s: %v", sess.BgID, err) // already gone: fine
		}
	}
	if err := d.store.DeleteSession(ctx, id); err != nil {
		return err
	}
	d.forget(id)
	return nil
}

func nameOf(s *core.Session) string {
	if n := s.DisplayName(); n != "" {
		return n
	}
	return s.ID
}
