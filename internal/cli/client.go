package cli

import (
	"bufio"
	"context"
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
		return &client{conn: conn}, nil
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
			return &client{conn: conn}, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("daemon didn't start (see %s): %w", p.DaemonLog(), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *client) close() { _ = c.conn.Close() }

func (c *client) snapshot() (ipc.Snapshot, error) {
	var s ipc.Snapshot
	err := c.conn.Call(ipc.OpSnapshot, nil, &s)
	return s, err
}

func (c *client) mutate(m ipc.Mutation, out any) error {
	return c.conn.Call(ipc.OpMutate, m, out)
}

// withClient runs fn with a connected client and a fresh snapshot.
func withClient(cmdCtx context.Context, fn func(*client, ipc.Snapshot) error) error {
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
func resolveSession(snap ipc.Snapshot, sel string) (*core.Session, error) {
	rows := core.Layout(snap.Sessions, snap.Groups, snap.ViewState)
	return core.Resolve(rows, snap.Sessions, sel)
}

// resolveGroup finds a live group by id or case-insensitive name ("default" works).
func resolveGroup(snap ipc.Snapshot, sel string) (*core.Group, error) {
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
