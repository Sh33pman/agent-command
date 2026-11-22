package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// requests makes the creating ops safe to retry (design 8.4): one that
// carries a request id runs once, and a retry with the same id within
// ipc.RequestKeep gets its result back, even after the daemon restarts.
// Retries that arrive while the first still runs wait for it. A failure
// isn't kept: trying again tries again.
type requests struct {
	mu      sync.Mutex
	running map[string]*flight
}

type flight struct {
	done chan struct{}
	out  json.RawMessage
	err  error
}

// retryable is the catalog name of req's op, when it takes a request id.
func retryable(req ipc.Request) (string, bool) {
	name := req.Op
	if name == catalog.MutateOp {
		var m ipc.Mutation
		if json.Unmarshal(req.Data, &m) != nil {
			return "", false
		}
		name = m.Kind
	}
	op, ok := catalog.Local.Lookup(name)
	return name, ok && op.Retryable
}

func (r *requests) once(ctx context.Context, d *Daemon, id, name string, req ipc.Request) (any, error) {
	r.mu.Lock()
	if f, ok := r.running[id]; ok {
		r.mu.Unlock()
		select {
		case <-f.done:
			return f.out, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	since := d.cfg.Now().Add(-ipc.RequestKeep)
	kept, err := d.store.Request(ctx, id, since)
	switch {
	case err == nil:
		r.mu.Unlock()
		if kept.Op != name {
			return nil, badRequest("request id %s was used for %s, not %s", id, kept.Op, name)
		}
		return json.RawMessage(kept.Result), nil
	case !errors.Is(err, store.ErrNotFound):
		r.mu.Unlock()
		return nil, err
	}
	f := &flight{done: make(chan struct{})}
	if r.running == nil {
		r.running = map[string]*flight{}
	}
	r.running[id] = f
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
		close(f.done)
	}()
	// The host keeps its own copy when the op goes on to one.
	out, err := d.run(ipc.WithRequestID(ctx, id), req)
	if err != nil {
		f.err = err
		return nil, err
	}
	if f.out, f.err = json.Marshal(onTheWire(out)); f.err != nil {
		return nil, f.err
	}
	now := d.cfg.Now()
	if err := d.store.SaveRequest(ctx, store.Request{ID: id, Op: name, Result: f.out, At: now}, now.Add(-ipc.RequestKeep)); err != nil {
		d.cfg.Log.Printf("keeping request %s: %v", id, err)
	}
	return f.out, nil
}
