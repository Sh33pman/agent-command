package hostapi

import (
	"cmp"
	"context"
	"encoding/json"
	"time"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Callable says whether a paired device may run op on this host through
// api.OpCall (see internal/remote/api), whatever its scope: not an op for the
// host's own machine, a stream, or one that uses the host's own hosts (so a
// host can't be used to reach its hosts). Its scope decides the rest
// (allowed).
func Callable(op catalog.Op) bool {
	switch {
	case op.HostOnly, op.Stream, op.Name == ipc.OpHello, usesHosts(op.Name):
		return false
	}
	return true
}

// usesHosts says whether an op is this machine using its own Remote hosts.
func usesHosts(name string) bool {
	switch name {
	case ipc.OpRemoteHosts, ipc.OpRemoteHostAdd, ipc.OpRemoteHostEdit, ipc.OpRemoteHostRemove, ipc.OpRemoteHostRetry,
		ipc.OpRemoteBrowse, ipc.OpRemoteNew,
		ipc.OpRemoteFoldersRecent, ipc.OpRemoteFoldersList, ipc.OpRemoteFoldersMkdir, ipc.OpRemoteHarnesses,
		ipc.OpRemoteCall, ipc.OpRemoteConfigGet, ipc.OpRemoteConfigSet:
		return true
	}
	return false
}

// rank orders scopes: each allows what the ones below it do.
func rank(scope string) int {
	return map[string]int{config.ScopeRead: 1, config.ScopeAct: 2, config.ScopeAdmin: 3}[scope]
}

// allowed checks a caller's scope against what an op needs.
func (a *API) allowed(c *wire.Call, need string) error {
	have := a.r.Scope(c.Conn.Peer.Fingerprint)
	if rank(have) >= rank(need) {
		return nil
	}
	return wire.Errorf(api.CodePermission, "%s needs %s, and this machine may only %s on the host: there, acmd remote scope %s %s",
		c.Op, need, have, cmp.Or(c.Conn.Peer.Name, c.Conn.Peer.Fingerprint), need)
}

// need serves an op only for a caller whose scope allows it.
func (a *API) need(scope string, h wire.Handler) wire.Handler {
	return func(ctx context.Context, c *wire.Call) (any, error) {
		if err := a.allowed(c, scope); err != nil {
			return nil, err
		}
		return h(ctx, c)
	}
}

func (a *API) needStream(scope string, h wire.StreamHandler) wire.StreamHandler {
	return func(ctx context.Context, c *wire.Call, s *wire.Stream) error {
		if err := a.allowed(c, scope); err != nil {
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
	if !Callable(op) {
		return nil, wire.Errorf(api.CodeForbidden, "%s can't be run from another machine", name)
	}
	if err := a.allowed(&wire.Call{Op: name, Conn: c.Conn}, string(op.Scope)); err != nil {
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
		if n.Harness, err = a.harness(n.Harness); err != nil {
			return nil, err
		}
		n.Cwd = dir
		if req.Data, err = json.Marshal(n); err != nil {
			return nil, err
		}
	}
	ctx = ipc.WithRequestID(context.WithValue(ctx, peerKey{}, c.Conn.Peer), req.RequestID)
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
