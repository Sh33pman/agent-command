package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// hostBackend is the daemon for the host API (internal/remote/hostapi), which
// decides what Hosting's clients may see: only Remote sessions.
type hostBackend struct{ d *Daemon }

var _ hostapi.Backend = hostBackend{}

func (b hostBackend) State(ctx context.Context) (hostapi.State, error) {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, true)
	if err != nil {
		return hostapi.State{}, err
	}
	inbox, err := d.store.InboxQueue(ctx)
	if err != nil {
		return hostapi.State{}, err
	}
	// Sessions added from other hosts are theirs to offer.
	sessions = slices.DeleteFunc(sessions, (*core.Session).OnHost)
	return hostapi.State{Sessions: sessions, Inbox: inbox, Notices: d.notices()}, nil
}

func (b hostBackend) Changes() (uint64, <-chan struct{}) { return b.d.changes.current() }

// NewSession starts a session as `acmd new` does, with this host's own
// claude_args or agy_args before the client's.
func (b hostBackend) NewSession(ctx context.Context, req ipc.NewRequest) (*core.Session, error) {
	d := b.d
	{
		c := d.config()
		var own []string
		switch req.Harness {
		case "", core.HarnessClaude:
			own = c.ClaudeArgs
		case core.HarnessAntigravity:
			own = c.AgyArgs
		}
		req.Args = append(slices.Clone(own), req.Args...)
	}
	s, err := d.newSession(ctx, req)
	if err == nil {
		d.changes.bump()
	}
	return s, err
}

func (b hostBackend) Rename(ctx context.Context, id, name string) error {
	return b.mutate(ctx, ipc.Mutation{Kind: ipc.MutRename, ID: id, Text: name})
}

func (b hostBackend) End(ctx context.Context, id string, finish bool) error {
	kind := ipc.MutArchive
	if finish {
		kind = ipc.MutFinish
	}
	return b.mutate(ctx, ipc.Mutation{Kind: kind, ID: id})
}

func (b hostBackend) mutate(ctx context.Context, m ipc.Mutation) error {
	_, err := b.d.mutate(ctx, m)
	if err == nil {
		b.d.changes.bump()
	}
	return err
}

func (b hostBackend) HostSocket(id string) string { return b.d.cfg.Paths.HostSocket(id) }

// Harnesses are what Config.Harnesses says is installed (every harness when
// it's nil), and default_harness.
func (b hostBackend) Harnesses() ([]string, string) {
	d := b.d
	installed := []string{core.HarnessClaude, core.HarnessAntigravity, core.HarnessTerminal}
	if d.cfg.Harnesses != nil {
		installed = d.cfg.Harnesses()
	}
	return installed, d.config().DefaultHarness
}

// Config is the host's settings, for its clients.
func (b hostBackend) Config() ipc.ConfigState { return b.d.configState() }

// Call runs a daemon op for a paired device (Direct Connect), as the socket
// would, with the error's code kept.
func (b hostBackend) Call(ctx context.Context, op string, data json.RawMessage) (json.RawMessage, error) {
	d := b.d
	who := &caller{remote: "a paired device"}
	if p, ok := hostapi.PeerOf(ctx); ok {
		who.remote = cmp.Or(p.Name, p.CommonName, p.Fingerprint, who.remote)
	}
	ctx = context.WithValue(ctx, callerKey{}, who)
	req := ipc.Request{Op: op, Data: data}
	out, err := d.handle(ctx, req)
	if err != nil {
		r := resultOf("", err)
		return nil, wire.Errorf(r.Code, "%s", r.Error)
	}
	if changesState(op) {
		d.changes.bump()
		d.audit(ctx, req)
	}
	if out == nil {
		return nil, nil
	}
	return json.Marshal(onTheWire(out))
}

// Subscribe is the daemon's event stream for a paired device.
func (b hostBackend) Subscribe(ctx context.Context, req ipc.SubscribeRequest, send func(ipc.Event) error) error {
	who := &caller{remote: "a paired device"}
	if p, ok := hostapi.PeerOf(ctx); ok {
		who.remote = cmp.Or(p.Name, p.CommonName, p.Fingerprint, who.remote)
	}
	return b.d.stream(context.WithValue(ctx, callerKey{}, who), req, send)
}

// SetConfig changes the host's settings for a client (config.set).
func (b hostBackend) SetConfig(set ipc.ConfigSet) (ipc.ConfigState, error) {
	return b.d.setConfig(set)
}
