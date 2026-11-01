package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// HostRequest says what a session host runs (SPEC §4.5).
type HostRequest struct {
	ID            string // the acmd session
	Dir           string
	Name, Prompt  string
	Args          []string
	ClaudeSession string // new session: the Claude session id to start with
	Resume        string // resume this Claude session instead
}

// Hosts starts session hosts: detached processes that run the session's CLI
// under a PTY. Start returns once the host process is launched.
type Hosts interface {
	Start(ctx context.Context, r HostRequest) error
}

// hostReady is how long a new host gets to listen; tests shorten it.
var hostReady = 10 * time.Second

// newUUID returns a random (version 4) UUID, the form Claude's --session-id takes.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// hostSession records a new hosted session and starts its host. Claude's
// session id is chosen here, so its hooks find the row from the first one.
func (d *Daemon) hostSession(ctx context.Context, req ipc.NewRequest) (*core.Session, error) {
	d.mu.Lock()
	s := &core.Session{Kind: core.KindHosted, Cwd: req.Cwd, ClaudeSessionID: newUUID(), CreatedAt: d.cfg.Now().UTC(), NameSource: core.NameAuto}
	core.Apply(s, core.Event{Kind: core.EventCreated, Time: d.cfg.Now()})
	if req.GroupID != "" {
		s.GroupID = req.GroupID
	}
	if req.Name != "" {
		s.Name, s.NameSource = req.Name, core.NameUser
	}
	if req.Description != "" {
		s.Description = req.Description
	}
	err := d.store.CreateSession(ctx, s)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := d.startHost(ctx, HostRequest{ID: s.ID, Dir: req.Cwd, Name: req.Name, Prompt: req.Prompt, Args: req.Args, ClaudeSession: s.ClaudeSessionID}); err != nil {
		d.mu.Lock()
		_ = d.store.DeleteSession(ctx, s.ID) // nothing ran: don't leave a ghost
		d.mu.Unlock()
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if req.ShowIn != "" {
		v, err := d.store.View(ctx, req.ShowIn)
		if err == nil {
			v.LastFocusedAt = d.cfg.Now()
			err = d.show(ctx, v, s.ID)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return s, nil
}

// startHost launches a host and waits until its socket answers.
func (d *Daemon) startHost(ctx context.Context, r HostRequest) error {
	if err := d.cfg.Hosts.Start(ctx, r); err != nil {
		return err
	}
	sock := d.cfg.Paths.HostSocket(r.ID)
	for deadline := time.Now().Add(hostReady); time.Now().Before(deadline); {
		if term.Running(sock) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("the session's host didn't start (see %s)", d.cfg.Paths.DaemonLog())
}

// resumeHosted runs `claude --resume` for a stopped hosted session in a new host.
func (d *Daemon) resumeHosted(ctx context.Context, s *core.Session) (*core.Session, error) {
	if !term.Running(d.cfg.Paths.HostSocket(s.ID)) {
		if err := d.startHost(ctx, HostRequest{ID: s.ID, Dir: s.Cwd, Resume: s.ClaudeSessionID}); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.store.Session(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	core.Apply(s, core.Event{Kind: core.EventResumed, Time: d.eventTime(s)})
	return s, d.store.UpdateSession(ctx, s)
}

// stopHost ends a hosted session's host (its CLI is hung up); the
// conversation is kept by the CLI.
func (d *Daemon) stopHost(s *core.Session) {
	if s.Kind == core.KindHosted {
		_ = term.Kill(d.cfg.Paths.HostSocket(s.ID)) // not running: fine
	}
}
