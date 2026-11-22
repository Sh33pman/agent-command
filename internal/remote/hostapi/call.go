package hostapi

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Callable says whether a paired device may run op on this host through
// api.OpCall (see internal/remote/api): Direct Connect, the whole host, with
// act scope at most.
func Callable(op catalog.Op) bool {
	switch {
	case op.HostOnly, op.Stream, op.Scope == catalog.ScopeAdmin:
		return false
	case op.Name == ipc.OpHello, strings.HasPrefix(op.Name, "remote."):
		return false
	}
	return true
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
	// The host's settings, as config.get and config.set are on their own:
	// not its device ones, which belong to its own machine.
	switch name {
	case ipc.OpConfigGet:
		return a.configGet(ctx, &wire.Call{Op: api.OpConfigGet})
	case ipc.OpConfigSet:
		return a.configSet(ctx, &wire.Call{Op: api.OpConfigSet, Args: req.Data})
	}
	op, ok := catalog.Local.Lookup(name)
	if !ok {
		return nil, wire.Errorf(wire.CodeUnknownOp, "this host has no %s", name)
	}
	if !Callable(op) {
		return nil, wire.Errorf(api.CodeForbidden, "%s can't be run from another machine", name)
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
		if n.Harness, err = a.harness(n.Harness); err != nil {
			return nil, err
		}
		n.Cwd = dir
		if req.Data, err = json.Marshal(n); err != nil {
			return nil, err
		}
	}
	data, err := a.b.Call(context.WithValue(ctx, peerKey{}, c.Conn.Peer), req.Op, req.Data)
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
	ctx, cancel := context.WithCancel(context.WithValue(ctx, peerKey{}, c.Conn.Peer))
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
