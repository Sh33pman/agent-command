package hostapi

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Who may run what is the catalog's rule (catalog.Caller.Allow), checked by
// the daemon's one dispatcher for every transport (design 6.12) through
// Backend.Allow. This transport only authenticates: each call carries its
// pairing's scope (CallerOf).

type callerKey struct{}

// CallerOf is the paired machine a request came from, as the catalog's rule
// sees it: its pairing's scope and name.
func CallerOf(ctx context.Context) (catalog.Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(catalog.Caller)
	return c, ok
}

// WithCaller is ctx carrying c as the caller a transport authenticated.
func WithCaller(ctx context.Context, c catalog.Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// authenticated is ctx with who c came from: its peer and its pairing.
func (a *API) authenticated(ctx context.Context, c *wire.Call) context.Context {
	p := c.Conn.Peer
	who := catalog.Caller{Scope: catalog.Scope(a.r.Scope(p.Fingerprint)), Name: cmp.Or(p.Name, p.Fingerprint)}
	return context.WithValue(context.WithValue(ctx, peerKey{}, p), callerKey{}, who)
}

// allow asks the daemon whether c's caller may run the catalog op name.
func (a *API) allow(ctx context.Context, name string) error {
	err := a.b.Allow(ctx, name)
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return wire.Errorf(coded.Code(), "%s", err.Error())
	}
	return err
}

// need serves a wire op as the catalog op name: checked as that op.
func (a *API) need(name string, h wire.Handler) wire.Handler {
	return func(ctx context.Context, c *wire.Call) (any, error) {
		ctx = a.authenticated(ctx, c)
		if err := a.allow(ctx, name); err != nil {
			return nil, err
		}
		return h(ctx, c)
	}
}

func (a *API) needStream(name string, h wire.StreamHandler) wire.StreamHandler {
	return func(ctx context.Context, c *wire.Call, s *wire.Stream) error {
		ctx = a.authenticated(ctx, c)
		if err := a.allow(ctx, name); err != nil {
			return err
		}
		return h(ctx, c, s)
	}
}

// call runs one of the daemon's ops for a paired device.
func (a *API) call(ctx context.Context, c *wire.Call) (any, error) {
	var req api.CallRequest
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	name := req.Op
	if name == catalog.MutateOp {
		var m ipc.Mutation
		if err := json.Unmarshal(req.Data, &m); err != nil {
			return nil, wire.Errorf(wire.CodeBadRequest, "mutate: %v", err)
		}
		name = m.Kind
	}
	op, ok := catalog.Local.Lookup(name)
	if !ok {
		return nil, wire.Errorf(wire.CodeUnknownOp, "this host has no %s", name)
	}
	// call answers once: a stream has its own op (subscribe, attach).
	if op.Stream || name == ipc.OpHello {
		return nil, wire.Errorf(api.CodeForbidden, "%s can't be run through call", name)
	}
	ctx = a.authenticated(ctx, c)
	if err := a.allow(ctx, name); err != nil {
		return nil, err
	}
	// The host's settings, as config.get and config.set are on their own:
	// not its device ones, which belong to its own machine.
	switch name {
	case ipc.OpConfigGet:
		return a.configGet(ctx, &wire.Call{Op: api.OpConfigGet})
	case ipc.OpConfigSet:
		return a.configSet(ctx, &wire.Call{Op: api.OpConfigSet, Args: req.Data})
	}
	if name == ipc.OpNew {
		// The folder is on this host: "~" and relative paths start at its
		// home. The harness is its default when none is named, and must be
		// installed here.
		var n ipc.NewRequest
		if err := json.Unmarshal(req.Data, &n); err != nil {
			return nil, wire.Errorf(wire.CodeBadRequest, "new: %v", err)
		}
		dir, err := a.folder(n.Cwd)
		if err != nil {
			return nil, err
		}
		if n.HarnessID, n.HarnessKind, err = a.harness(n.HarnessID, n.HarnessKind); err != nil {
			return nil, err
		}
		n.Cwd = dir
		if req.Data, err = json.Marshal(n); err != nil {
			return nil, err
		}
	}
	ctx = ipc.WithClient(ipc.WithRequestID(ctx, req.RequestID), req.Client)
	data, err := a.b.Call(ctx, req.Op, req.Data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

type peerKey struct{}

// PeerOf is the paired device a call (api.OpCall) came from, for the
// daemon's record of who made each change.
func PeerOf(ctx context.Context) (wire.Peer, bool) {
	p, ok := ctx.Value(peerKey{}).(wire.Peer)
	return p, ok
}

// subscribe is the host daemon's event stream, for a paired device.
func (a *API) subscribe(ctx context.Context, c *wire.Call, s *wire.Stream) error {
	var req ipc.SubscribeRequest
	if err := c.Decode(&req); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // the client goes: its end closes
		defer cancel()
		_, _ = s.Read(make([]byte, 1))
	}()
	return a.b.Subscribe(ctx, req, func(ev ipc.Event) error {
		_ = s.SetWriteDeadline(time.Now().Add(eventWriteTimeout))
		return s.Send(ev)
	})
}

// AllowByCatalog is the catalog's rule for the caller ctx carries: what the
// daemon's dispatcher checks. Test backends use it to check as the daemon does.
func AllowByCatalog(ctx context.Context, name string) error {
	op, ok := catalog.Local.Lookup(name)
	if !ok {
		return wire.Errorf(wire.CodeUnknownOp, "this host has no %s", name)
	}
	c, _ := CallerOf(ctx)
	return c.Allow(op)
}
