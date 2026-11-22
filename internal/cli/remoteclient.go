package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
	"github.com/brettmostert/agent-command/internal/version"
)

// `acmd remote add|edit|remove|list|retry|browse|open|attach|drop`: this
// machine as a client of Remote hosts. The daemon keeps the connections; these
// ask it over its socket, and attach to an added session's local socket.

// Exit codes for a Remote host that can't be used, so scripts can tell why.
// 1 is any other failure.
const (
	exitUnreachable  = 3 // no answer, or the connection dropped
	exitPending      = 4 // waiting for the host's user to approve this machine
	exitUnauthorized = 5 // the host refused this machine
	exitFingerprint  = 6 // the host's fingerprint isn't the pinned one
	exitVersion      = 7 // the two speak different protocol versions
	exitPortTaken    = 8 // Hosting is on, but can't listen on its port
)

func remoteClientCmds() []*cobra.Command {
	return []*cobra.Command{newRemoteAddCmd(), newRemoteEditCmd(), newRemoteRemoveCmd(), newRemoteListCmd(),
		newRemoteRetryCmd(), newRemoteBrowseCmd(), newRemoteAttachCmd()}
}

// hostExit is the exit code for a host in state, 0 when the state isn't a
// failure to connect. A host still connecting after a failed call is retrying
// one it couldn't reach.
func hostExit(state string) int {
	switch state {
	case ipc.HostUnreachable, ipc.HostConnecting:
		return exitUnreachable
	case ipc.HostPending:
		return exitPending
	case ipc.HostUnauthorized:
		return exitUnauthorized
	case ipc.HostFingerprint:
		return exitFingerprint
	case ipc.HostVersion:
		return exitVersion
	}
	return 0
}

// hostProblem explains why h can't be used, with its exit code; nil when its
// state isn't a failure to connect.
func hostProblem(h ipc.RemoteHost) error {
	code := hostExit(h.State)
	if code == 0 {
		return nil
	}
	var msg string
	switch h.State {
	case ipc.HostPending:
		msg = fmt.Sprintf("%s is waiting for approval: on %s, approve this machine with acmd remote requests (your fingerprint is %s)",
			h.Name, h.Name, h.OurFingerprint)
	case ipc.HostUnauthorized:
		msg = fmt.Sprintf("%s refused this machine: on %s, run acmd remote authorize %s", h.Name, h.Name, h.OurFingerprint)
	case ipc.HostFingerprint:
		msg = fmt.Sprintf("%s's fingerprint doesn't match: it shows %s, but you pinned %s. "+
			"If its identity changed on purpose, run acmd remote edit %s --fingerprint %s", h.Name, h.Seen, h.Fingerprint, h.Name, h.Seen)
	case ipc.HostVersion:
		msg = fmt.Sprintf("%s speaks another protocol version: %s", h.Name, cmpOr(h.Error, "update acmd on both machines"))
	default:
		msg = fmt.Sprintf("%s (%s) can't be reached", h.Name, h.Address)
		if h.Error != "" {
			msg += ": " + h.Error
		}
	}
	return &ExitError{Code: code, Err: errors.New(msg)}
}

func cmpOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// withDaemon runs fn on a connection to the daemon, starting it when needed.
func withDaemon(cmd *cobra.Command, fn func(*client) error) error {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	return fn(c)
}

// hostCall runs op, which needs host's connection. When it fails because the
// host can't be used, the error says why, with that exit code.
func (c *client) hostCall(host, op string, in, out any) error {
	err := c.conn.Call(op, in, out)
	if err == nil {
		return nil
	}
	if h, ok := c.knownHost(host); ok {
		if p := hostProblem(h); p != nil {
			return p
		}
	}
	return err
}

// knownHost is the known host named name, as the daemon sees it now.
func (c *client) knownHost(name string) (ipc.RemoteHost, bool) {
	var hosts []ipc.RemoteHost
	if c.conn.Call(ipc.OpRemoteHosts, nil, &hosts) != nil {
		return ipc.RemoteHost{}, false
	}
	for _, h := range hosts {
		if h.Name == name {
			return h, true
		}
	}
	return ipc.RemoteHost{}, false
}

func newRemoteAddCmd() *cobra.Command {
	var fingerprint, cert string
	cmd := &cobra.Command{
		Use:   "add <name> <address> (--fingerprint SHA256:…|--cert file)",
		Short: "Add a Remote host: another acmd whose sessions you want to use here",
		Long: "Adds a Remote host to [[remote.hosts]] in config.toml. The address is host:port (Hosting's\n" +
			"port is 7433 unless the host says otherwise). Pin the host by the fingerprint its\n" +
			"`acmd remote identity` shows, or by its cert file. The host must also authorize this\n" +
			"machine: the output shows the fingerprint to give it.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (fingerprint == "") == (cert == "") {
				return errors.New("give the host's --fingerprint or its --cert file (one of them)")
			}
			e := ipc.RemoteHostEdit{Name: args[0], Address: args[1], Fingerprint: fingerprint}
			if cert != "" {
				abs, err := filepath.Abs(cert) // the daemon reads it, from another folder
				if err != nil {
					return err
				}
				e.Cert = abs
			}
			var h ipc.RemoteHost
			if err := withDaemon(cmd, func(c *client) error { return c.conn.Call(ipc.OpRemoteHostAdd, e, &h) }); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Added the Remote host %q (%s), pinned to %s.\n", h.Name, h.Address, h.Fingerprint)
			if h.OurFingerprint != "" {
				fmt.Fprintf(out, "On %s, authorize this machine: acmd remote authorize %s\n", h.Name, h.OurFingerprint)
			}
			fmt.Fprintf(out, "Then see its Remote sessions with: acmd remote browse %s\n", h.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&fingerprint, "fingerprint", "", "the host's fingerprint (SHA256:…), from its acmd remote identity")
	cmd.Flags().StringVar(&cert, "cert", "", "the host's cert file, to pin its fingerprint from")
	return cmd
}

func newRemoteEditCmd() *cobra.Command {
	var e ipc.RemoteHostEdit
	cmd := &cobra.Command{
		Use:               "edit <name> [--name N] [--address host:port] [--fingerprint SHA256:…|--cert file]",
		Short:             "Change a Remote host: its name, address or pinned fingerprint",
		Long:              "Changes a Remote host in [[remote.hosts]]. Its added sessions move along when it's renamed.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeHosts,
		RunE: func(cmd *cobra.Command, args []string) error {
			if e.Name == "" && e.Address == "" && e.Fingerprint == "" && e.Cert == "" {
				return errors.New("say what to change: --name, --address, --fingerprint or --cert")
			}
			if e.Fingerprint != "" && e.Cert != "" {
				return errors.New("give --fingerprint or --cert, not both")
			}
			if e.Cert != "" {
				abs, err := filepath.Abs(e.Cert)
				if err != nil {
					return err
				}
				e.Cert = abs
			}
			e.Host = args[0]
			var h ipc.RemoteHost
			if err := withDaemon(cmd, func(c *client) error { return c.conn.Call(ipc.OpRemoteHostEdit, e, &h) }); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Changed the Remote host %q: %s, pinned to %s.\n", h.Name, h.Address, h.Fingerprint)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&e.Name, "name", "", "a new name")
	f.StringVar(&e.Address, "address", "", "a new address (host:port)")
	f.StringVar(&e.Fingerprint, "fingerprint", "", "pin this fingerprint instead")
	f.StringVar(&e.Cert, "cert", "", "pin the fingerprint of this cert file instead")
	return cmd
}

func newRemoteRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:               "remove <name>",
		Short:             "Forget a Remote host, and the sessions added from it here",
		Long:              "Removes a Remote host from config.toml. Its added sessions are removed here only: they keep running on the host.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeHosts,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withDaemon(cmd, func(c *client) error {
				h, ok := c.knownHost(args[0])
				if !ok {
					return fmt.Errorf("there's no Remote host named %q (see acmd remote list)", args[0])
				}
				q := fmt.Sprintf("Remove the Remote host %q?", h.Name)
				if h.Sessions > 0 {
					q = fmt.Sprintf("Remove the Remote host %q? Its %s added here are removed here only; they keep running on %s.",
						h.Name, plural(h.Sessions, "session"), h.Name)
				}
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				if err := c.conn.Call(ipc.OpRemoteHostRemove, ipc.RemoteHostRef{Host: h.Name}, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Removed the Remote host %q. Its sessions on %s weren't touched.\n", h.Name, h.Name)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newRemoteListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Remote hosts this machine knows, and their connections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var hosts []ipc.RemoteHost
			if err := withDaemon(cmd, func(c *client) error { return c.conn.Call(ipc.OpRemoteHosts, nil, &hosts) }); err != nil {
				return err
			}
			if jsonOut {
				if hosts == nil {
					hosts = []ipc.RemoteHost{}
				}
				return printJSON(jsonWriter(cmd), hosts)
			}
			return writeRemoteHosts(cmd.OutOrStdout(), hosts)
		},
	}
	return cmd
}

func writeRemoteHosts(w io.Writer, hosts []ipc.RemoteHost) error {
	if len(hosts) == 0 {
		_, err := fmt.Fprintln(w, "No Remote hosts. Add one with: acmd remote add <name> <host:port> --fingerprint SHA256:…")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tADDRESS\tSTATE\tSESSIONS\tNOTE")
	for _, h := range hosts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", h.Name, h.Address, h.State, h.Sessions, hostNote(h))
	}
	return tw.Flush()
}

// hostNote says more about a host's state, for acmd remote list.
func hostNote(h ipc.RemoteHost) string {
	switch h.State {
	case ipc.HostConnected:
		if h.HostVersion != "" {
			return fmt.Sprintf("%s, acmd %s", cmpOr(h.HostName, h.Name), h.HostVersion)
		}
		return ""
	case ipc.HostPending:
		return "waiting for approval — your fingerprint is " + h.OurFingerprint
	case ipc.HostFingerprint:
		return "fingerprint mismatch: it shows " + h.Seen
	}
	return h.Error
}

// retryWait is how long acmd remote retry waits for the host to answer.
var retryWait = 15 * time.Second

func newRemoteRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <name>",
		Short: "Connect to a Remote host now, without waiting to retry, and say how it went",
		Long: "Connects to a Remote host now, even one whose fingerprint didn't match, and waits until\n" +
			"it's connected or refuses. It exits with the code for why it can't be used (see acmd remote --help).",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeHosts,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withDaemon(cmd, func(c *client) error {
				var h ipc.RemoteHost
				if err := c.conn.Call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: args[0]}, &h); err != nil {
					return err
				}
				// Wait for this try: until the state moves on from what it was
				// (a failure stays the same state, but a try goes through
				// connecting, which changes Since), and isn't connecting.
				first := h
				settled := func() bool {
					if h.State == ipc.HostConnecting || h.State == ipc.HostIdle {
						return false
					}
					return h.State == ipc.HostConnected || !h.Since.Equal(first.Since) || first.State == ipc.HostConnecting
				}
				for deadline := time.Now().Add(retryWait); !settled() && time.Now().Before(deadline); {
					time.Sleep(100 * time.Millisecond)
					var ok bool
					if h, ok = c.knownHost(args[0]); !ok {
						return fmt.Errorf("there's no Remote host named %q", args[0])
					}
				}
				if h.State == ipc.HostConnected {
					fmt.Fprintf(cmd.OutOrStdout(), "Connected to %s (%s).\n", h.Name, hostNote(h))
					return nil
				}
				if p := hostProblem(h); p != nil {
					return p
				}
				if h.State == ipc.HostError {
					return fmt.Errorf("%s: %s", h.Name, h.Error)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s is %s.\n", h.Name, h.State)
				return nil
			})
		},
	}
}

// hostSessions are a host's sessions, as its tree shows here: the most
// recently active first.
func (c *client) hostSessions(host string) ([]*core.Session, error) {
	var b ipc.RemoteBrowse
	if err := c.hostCall(host, ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: host}, &b); err != nil {
		return nil, err
	}
	return sessionsOn(snapshotOf(ipc.Snapshot{Sessions: b.Sessions}), host), nil
}

func sessionsOn(snap snapshot, host string) []*core.Session {
	var out []*core.Session
	for _, s := range snap.Sessions {
		if s.Host == host && s.Status != core.StatusArchived {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *core.Session) int {
		return cmp.Or(cmpTime(b.LastEventAt, b.CreatedAt).Compare(cmpTime(a.LastEventAt, a.CreatedAt)), strings.Compare(a.ID, b.ID))
	})
	return out
}

func newRemoteBrowseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "browse <host>",
		Short:             "List a Remote host's sessions, as its tree shows here",
		Long:              "Lists the sessions a Remote host has, the most recently active first. Attach to one with acmd remote attach <host> <#|id|name>.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeHosts,
		RunE: func(cmd *cobra.Command, args []string) error {
			var list []*core.Session
			err := withDaemon(cmd, func(c *client) error {
				var err error
				list, err = c.hostSessions(args[0])
				return err
			})
			if err != nil {
				return err
			}
			if done, err := emitJSON(cmd, ipc.SessionsOf(list)); done {
				return err
			}
			return writeBrowse(cmd.OutOrStdout(), args[0], list)
		},
	}
	return cmd
}

func writeBrowse(w io.Writer, host string, list []*core.Session) error {
	if len(list) == 0 {
		_, err := fmt.Fprintf(w, "%s has no sessions yet, or hasn't said: see acmd remote list.\n", host)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTATUS\tNAME\tHARNESS\tDIR\tLAST\tID")
	for i, s := range list {
		status := string(s.Status)
		if s.Unseen {
			status += "•"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", i+1, status, display(s.Name, 40), s.Harness, filepath.Base(s.Cwd),
			ago(cmpTime(s.LastEventAt, s.CreatedAt)), s.ID)
	}
	return tw.Flush()
}

func cmpTime(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

// ago is how long ago t was, as acmd list shows it.
func ago(t time.Time) string {
	return age(&core.Session{LastEventAt: t})
}

// resolveRemoteSession finds one of a host's sessions by its id, its number
// in acmd remote browse (# optional), its name, or a unique prefix of its
// name or id.
func resolveRemoteSession(host string, list []*core.Session, sel string) (*core.Session, error) {
	sel = strings.TrimSpace(sel)
	notFound := fmt.Errorf("%s has no session %q (see acmd remote browse %s)", host, sel, host)
	if sel == "" {
		return nil, notFound
	}
	for _, s := range list {
		if s.ID == sel {
			return s, nil
		}
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(sel, "#")); err == nil {
		if n < 1 || n > len(list) {
			return nil, fmt.Errorf("%s has no session number %d (see acmd remote browse %s)", host, n, host)
		}
		return list[n-1], nil
	}
	var exact, prefix []*core.Session
	lower := strings.ToLower(sel)
	for _, s := range list {
		name := strings.ToLower(s.Name)
		switch {
		case name == lower:
			exact = append(exact, s)
		case strings.HasPrefix(name, lower) || strings.HasPrefix(s.ID, sel):
			prefix = append(prefix, s)
		}
	}
	for _, m := range [][]*core.Session{exact, prefix} {
		switch len(m) {
		case 0:
			continue
		case 1:
			return m[0], nil
		}
		names := make([]string, len(m))
		for i, s := range m {
			names[i] = fmt.Sprintf("%s (%s)", cmpOr(s.Name, "(unnamed)"), s.ID)
		}
		return nil, fmt.Errorf("%q matches more than one session on %s: %s", sel, host, strings.Join(names, ", "))
	}
	return nil, notFound
}

// findRemoteSession resolves sel among host's sessions.
func (c *client) findRemoteSession(host, sel string) (*core.Session, error) {
	list, err := c.hostSessions(host)
	if err != nil {
		return nil, err
	}
	return resolveRemoteSession(host, list, sel)
}

// attachTerminal attaches this terminal to a session's socket until it's
// detached; swappable in tests.
var attachTerminal = func(ctx context.Context, socket string) error {
	err := attachHost(ctx, socket, term.ModeStream, detachKey)
	if errors.Is(err, errNoHost) {
		return errors.New("the session's socket isn't there: is the daemon running?")
	}
	return err
}

// stdinTerminal says whether stdin is a terminal to attach; swappable in tests.
var stdinTerminal = func(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && isTerminal(f)
}

// attachLocal attaches to the session id's socket here, saying how to detach.
func attachLocal(cmd *cobra.Command, id, what string) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Attached to %s. Detach with Ctrl+].\r\n", what)
	err = attachTerminal(cmd.Context(), p.HostSocket(id))
	fmt.Fprintf(cmd.ErrOrStderr(), "\r\nDetached from %s.\n", what)
	return err
}

func sessionWhat(host string, s *core.Session) string {
	return fmt.Sprintf("%s on %s", cmpOr(strconv.Quote(s.Name), "session "+s.ID), host)
}

func newRemoteAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "attach <host> <#|id|name>",
		Aliases: []string{"open"},
		Short:   "Attach this terminal to one of a Remote host's sessions",
		Long: "Attaches this terminal to one of a Remote host's sessions, through this machine's daemon: Ctrl+]\n" +
			"detaches. The session is its number in acmd remote browse, its id, or its name. A host's sessions\n" +
			"show here, and in the app, as the host has them.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeHostSessions,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdinTerminal(cmd) {
				return errors.New("acmd remote attach needs a terminal")
			}
			host := args[0]
			var s *core.Session
			err := withDaemon(cmd, func(c *client) error {
				var err error
				s, err = c.findRemoteSession(host, args[1])
				return err
			})
			if err != nil {
				return err
			}
			return attachLocal(cmd, s.ID, sessionWhat(host, s))
		},
	}
}

// Shell completion. It never starts the daemon, and gives up quickly.

// completeHosts completes a Remote host's name, from config.toml.
func completeHosts(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return hostNames(), cobra.ShellCompDirectiveNoFileComp
}

func hostNames() []string {
	p, err := paths.Default()
	if err != nil {
		return nil
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil
	}
	var names []string
	for _, h := range cfg.Remote.Hosts {
		names = append(names, h.Name)
	}
	return names
}

// completeHostSessions completes a host, then one of its sessions.
func completeHostSessions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeHosts(cmd, args, toComplete)
	case 1:
		return hostSessionNames(cmd.Context(), args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completionWait bounds what completion asks the daemon.
const completionWait = 3 * time.Second

// runningDaemon connects to a daemon that's running, without starting one.
func runningDaemon(parent context.Context) (*ipc.Conn, context.CancelFunc, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, completionWait)
	p, err := paths.Default()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.HelloFrom(version.Get().Version))
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		cancel()
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close(); cancel() }, nil
}

// hostSessionNames are host's sessions, by name (or id, for unnamed ones),
// each described by the other.
func hostSessionNames(ctx context.Context, host string) []string {
	conn, done, err := runningDaemon(ctx)
	if err != nil {
		return nil
	}
	defer done()
	var wire ipc.Snapshot
	if conn.Call(ipc.OpSnapshot, nil, &wire) != nil {
		return nil
	}
	var out []string
	for _, s := range sessionsOn(snapshotOf(wire), host) {
		if s.Name != "" && !strings.ContainsAny(s.Name, " \t") {
			out = append(out, s.Name+"\t"+s.ID)
		}
		out = append(out, s.ID+"\t"+cmpOr(s.Name, "(unnamed)"))
	}
	return out
}
