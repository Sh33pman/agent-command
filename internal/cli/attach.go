package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

// `acmd attach`: this terminal shows a session, local or a host's, until
// Ctrl+] detaches. Either way it attaches to the session's socket here: a
// local session's host, or the daemon's relay to a host's (as acmd remote
// attach does).

// sessionRunning says whether a session's socket answers; swappable in tests.
var sessionRunning = term.Running

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <#|id|name>",
		Short: "Attach this terminal to a session, here or on a host (Ctrl+] detaches)",
		Long: "Attaches this terminal to a session until Ctrl+] detaches. The session is its number in acmd list, its\n" +
			"id or its name: one here, or a host's, as its sessions show here. With --host, it's one of that host's\n" +
			"sessions, as the host numbers them. With --json, it prints the session once detached.",
		Example: "  acmd attach 2\n  acmd attach api\n  acmd attach --host studio docs",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdinTerminal(cmd) {
				return errors.New("acmd attach needs a terminal")
			}
			// Registered while attached: notices can be routed to this terminal.
			c, err := connectTerminal(cmd.Context())
			if err != nil {
				return err
			}
			defer c.close()
			s, err := c.attachTarget(args[0])
			if err != nil {
				return err
			}
			p, err := paths.Default()
			if err != nil {
				return err
			}
			what := attachWhat(s)
			if !sessionRunning(p.HostSocket(s.ID)) {
				return fmt.Errorf("%s isn't running: resume it with acmd resume %s", what, s.ID)
			}
			if err := attachLocal(cmd, s.ID, what); err != nil {
				return err
			}
			_, err = emitJSON(cmd, ipc.SessionOf(s))
			return err
		},
	}
}

// attachTarget is the session sel names: with --host, among that host's own
// sessions; otherwise among the sessions here, a host's included.
func (c *client) attachTarget(sel string) (*core.Session, error) {
	if c.host != "" {
		return c.findRemoteSession(c.host, sel)
	}
	snap, err := c.snapshot()
	if err != nil {
		return nil, err
	}
	snap.Sessions = visibleSessions(snap.Sessions)
	return resolveSession(snap, sel)
}

// attachWhat names s for the attached and detached lines.
func attachWhat(s *core.Session) string {
	if s.Host != "" {
		return sessionWhat(s.Host, s)
	}
	if s.Name != "" {
		return strconv.Quote(s.Name)
	}
	return "session " + s.ID
}
