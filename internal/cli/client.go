package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/hook"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

// client talks to the daemon, starting it (or replacing one of another
// version) when needed.
type client struct {
	conn *ipc.Conn
	// host is the Remote host the command runs against (--host), "" for this
	// machine.
	host string
}

// targetHost is --host, or ACMD_HOST: the Remote host commands run against.
var targetHost string

// call runs a daemon op: this machine's, or with --host the host's, through
// this machine's daemon (remote.call), which the host answers as its own
// socket would.
//
// With --json, its answer is kept to print (record).
func (c *client) call(op string, in, out any) error {
	var raw json.RawMessage
	if c.host == "" {
		if err := c.conn.Call(op, in, &raw); err != nil {
			return err
		}
	} else {
		rc := ipc.RemoteCall{Host: c.host, Op: op}
		if in != nil {
			b, err := json.Marshal(in)
			if err != nil {
				return err
			}
			rc.Data = b
		}
		if err := c.hostCall(c.host, ipc.OpRemoteCall, rc, &raw); err != nil {
			return err
		}
	}
	record(op, raw)
	if out == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// spawnDaemon is swappable in tests.
var spawnDaemon = hook.SpawnDaemon

func connect(ctx context.Context) (*client, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	me := ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}
	conn, err := ipc.Dial(ctx, p.Socket(), me)
	if err == nil {
		return &client{conn: conn, host: targetHost}, nil
	}
	var mismatch *ipc.ErrVersionMismatch
	if errors.As(err, &mismatch) {
		_ = conn.Call(ipc.OpShutdown, nil, nil)
		_ = conn.Close()
		waitDaemonGone(ctx, p)
	}
	if err := spawnDaemon(p); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err = ipc.Dial(ctx, p.Socket(), me)
		if err == nil {
			return &client{conn: conn, host: targetHost}, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("daemon didn't start (see %s): %w", p.DaemonLog(), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *client) close() { _ = c.conn.Close() }

func (c *client) snapshot() (snapshot, error) {
	var s ipc.Snapshot
	err := c.call(ipc.OpSnapshot, nil, &s)
	return snapshotOf(s), err
}

// snapshot is the daemon's snapshot with its sessions, groups and view as
// internal/core has them, for the CLI's layout and lookups.
type snapshot struct {
	ipc.Snapshot
	Sessions  []*core.Session `json:"-"`
	Groups    []*core.Group   `json:"-"`
	ViewState core.ViewState  `json:"-"`
}

func snapshotOf(s ipc.Snapshot) snapshot {
	return snapshot{Snapshot: s, Sessions: s.CoreSessions(), Groups: s.CoreGroups(), ViewState: s.ViewState.Core()}
}

func (c *client) mutate(m ipc.Mutation, out any) error {
	return c.call(ipc.OpMutate, m, out)
}

// withClient runs fn with a connected client and a fresh snapshot.
func withClient(cmdCtx context.Context, fn func(*client, snapshot) error) error {
	ctx, cancel := context.WithTimeout(cmdCtx, 30*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	snap, err := c.snapshot()
	if err != nil {
		return err
	}
	snap.Sessions = visibleSessions(snap.Sessions)
	return fn(c, snap)
}

// visibleSessions is what commands see; swappable in tests, whose sessions come
// from hooks rather than hosts.
var visibleSessions = hostedSessions

// hostedSessions keeps the sessions the app runs, as its sidebar does. The rest are
// rows left over from v1, which tracked sessions it didn't start.
func hostedSessions(sessions []*core.Session) []*core.Session {
	var hosted []*core.Session
	for _, s := range sessions {
		if s.Kind == core.KindHosted {
			hosted = append(hosted, s)
		}
	}
	return hosted
}

// resolveSession finds a session by number (current view), id, short id,
// Claude id or unique name prefix.
func resolveSession(snap snapshot, sel string) (*core.Session, error) {
	rows := core.Layout(snap.Sessions, snap.Groups, snap.ViewState)
	return core.Resolve(rows, snap.Sessions, sel)
}

// resolveGroup finds a live group by id or case-insensitive name ("default" works).
func resolveGroup(snap snapshot, sel string) (*core.Group, error) {
	sel = strings.TrimSpace(sel)
	for _, g := range snap.Groups {
		if g.ID == sel {
			return g, nil
		}
	}
	for _, g := range snap.Groups {
		if g.ArchivedAt.IsZero() && strings.EqualFold(g.Name, sel) {
			return g, nil
		}
	}
	return nil, fmt.Errorf("no group named %q", sel)
}

// confirm asks a yes/no question on a terminal; without a terminal it
// requires --yes.
func confirm(in io.Reader, out io.Writer, yes bool, question string) error {
	if yes {
		return nil
	}
	if f, ok := in.(*os.File); !ok || !isTerminal(f) {
		return errors.New("refusing without confirmation: pass --yes")
	}
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		return nil
	}
	return errors.New("canceled")
}

// isTerminal says whether f is a terminal. A character device isn't enough:
// /dev/null is one, and it's the input of anything the app or launchd runs.
func isTerminal(f *os.File) bool {
	return xterm.IsTerminal(f.Fd())
}
