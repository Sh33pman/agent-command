package daemon

import (
	"context"
	"slices"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
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
	if c, err := d.loadConfig(); err == nil {
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
	def := ""
	if c, err := d.loadConfig(); err == nil {
		def = c.DefaultHarness
	}
	return installed, def
}

// loadConfig reads config.toml afresh, so a change applies at once.
func (d *Daemon) loadConfig() (config.Config, error) {
	if d.cfg.ConfigFile == "" {
		return config.Default(), nil
	}
	return config.Load(d.cfg.ConfigFile)
}
