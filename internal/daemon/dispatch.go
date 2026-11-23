package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// One dispatcher for every transport (design 6.12): the local socket and the
// Remote API's call both run ops here. A transport only authenticates its
// caller and carries requests; what the caller may run is the catalog's rule
// (catalog.Caller.Allow), checked here, and a change is recorded here with
// who made it. Named pipes, HTTP and WebSockets plug in the same way.

// dispatch runs req for the caller ctx carries.
func (d *Daemon) dispatch(ctx context.Context, req ipc.Request) (any, error) {
	name, err := opName(req)
	if err != nil {
		return nil, err
	}
	if err := d.allow(ctx, name); err != nil {
		return nil, err
	}
	out, err := d.handle(ctx, req)
	if err == nil && changesState(req.Op) {
		d.changes.bump()
		d.audit(ctx, req)
	}
	return out, err
}

// allow says whether ctx's caller may run the catalog op name.
func (d *Daemon) allow(ctx context.Context, name string) error {
	op, ok := catalog.Local.Lookup(name)
	if !ok {
		return &codedError{code: ipc.CodeUnknownOp, err: fmt.Errorf("unknown op %q", name)}
	}
	if d.cfg.ClientOnly && op.NeedsSessions {
		return &codedError{code: ipc.CodeNotSupported, err: fmt.Errorf(
			"%s isn't supported here: acmd on %s only connects to hosts, and runs no sessions of its own. Use a host: acmd remote add", name, runtime.GOOS)}
	}
	return callerOf(ctx).catalog().Allow(op)
}

// catalog is the caller as the catalog's rule sees it.
func (c *caller) catalog() catalog.Caller {
	return catalog.Caller{Local: c.local, Scope: catalog.Scope(c.scope), Name: c.remote}
}

// opName is the catalog's name for req: a mutation's kind, else its op.
func opName(req ipc.Request) (string, error) {
	if req.Op != ipc.OpMutate {
		return req.Op, nil
	}
	var m struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(req.Data, &m); err != nil {
		return "", badRequest("bad mutation: %v", err)
	}
	if m.Kind == "" {
		return "", badRequest("a mutation names its kind")
	}
	return m.Kind, nil
}
