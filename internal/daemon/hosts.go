package daemon

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

// HostRequest says what a session host runs.
type HostRequest struct {
	ID            string // the acmd session
	Harness       string // "" = claude
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
	s := &core.Session{Kind: core.KindHosted, Cwd: req.Cwd, Harness: req.Harness, CreatedAt: d.cfg.Now().UTC(), NameSource: core.NameAuto}
	switch {
	case req.Resume != "":
		s.ClaudeSessionID = req.Resume
	case !s.IsTerminal():
		s.ClaudeSessionID = newUUID()
	}
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
	host := HostRequest{ID: s.ID, Harness: req.Harness, Dir: req.Cwd, Name: req.Name, Prompt: req.Prompt, Args: req.Args, ClaudeSession: s.ClaudeSessionID}
	if req.Resume != "" {
		host.ClaudeSession, host.Resume = "", req.Resume
	}
	if err := d.startHost(ctx, host); err != nil {
		d.mu.Lock()
		_ = d.store.DeleteSession(ctx, s.ID) // nothing ran: don't leave a ghost
		d.mu.Unlock()
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if s.IsTerminal() {
		s.Status = core.StatusRunning
		if err := d.store.UpdateSession(ctx, s); err != nil {
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

// resumeHosted runs `claude --resume` for a stopped hosted session in a new
// host, with a prompt to send and extra flags if given (a scheduled run). A
// terminal that a reboot ended gets a new shell in its folder.
func (d *Daemon) resumeHosted(ctx context.Context, s *core.Session, prompt string, args []string) (*core.Session, error) {
	if !term.Running(d.cfg.Paths.HostSocket(s.ID)) {
		r := HostRequest{ID: s.ID, Harness: s.Harness, Dir: s.Cwd, Resume: s.ClaudeSessionID, Prompt: prompt, Args: args}
		if err := d.startHost(ctx, r); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.store.Session(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	s.Kind, s.BgID, s.External = core.KindHosted, "", nil
	if !s.Stopped() { // a `--bg` session stopped just now
		core.Apply(s, core.Event{Kind: core.EventAgentStatus, AgentStatus: core.AgentStopped, Time: d.eventTime(s)})
	}
	core.Apply(s, core.Event{Kind: core.EventResumed, Time: d.eventTime(s)})
	if s.IsTerminal() {
		s.Status = core.StatusRunning
	}
	return s, d.store.UpdateSession(ctx, s)
}

// stopHost ends a hosted session's host (its CLI is hung up); the
// conversation is kept by the CLI.
func (d *Daemon) stopHost(s *core.Session) {
	if s.Kind == core.KindHosted {
		_ = term.Kill(d.cfg.Paths.HostSocket(s.ID)) // not running: fine
	}
}

// sweepHosts removes the id files of hosts that are gone (a reboot or a
// crash leaves them); hosts still running are left alone, and keep serving
// their session (they are separate processes). The sessions
// whose host is gone were paused when the daemon started.
func sweepHosts(p paths.Paths) {
	files, _ := filepath.Glob(filepath.Join(p.RuntimeDir, "h-*.id"))
	for _, f := range files {
		b, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			continue
		}
		if !term.Running(p.HostSocket(strings.TrimSpace(string(b)))) {
			_ = os.Remove(f)
			_ = os.Remove(strings.TrimSuffix(f, ".id") + ".sock")
		}
	}
}
