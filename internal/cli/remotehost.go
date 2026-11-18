package cli

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// `acmd remote hosting|status|clients|authorize|revoke|disconnect|requests`:
// Hosting, run by the daemon. These ask it over its socket.

// remoteCall runs one remote.* op on the daemon, starting it when needed.
func remoteCall(cmd *cobra.Command, op string, in, out any) error {
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
	return c.conn.Call(op, in, out)
}

func printJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func newRemoteHostingCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "hosting on|off",
		Short: "Turn Hosting on or off: whether other acmds can connect to this one",
		Long: "Hosting is this machine's listener for other acmds (clients). It's off by default, and\n" +
			"listens on localhost:7433 unless [remote.host] in config.toml says otherwise. When the\n" +
			"port is taken, it uses another (port_fallback) or fails with exit code 8: acmd remote\n" +
			"status shows the port in use. Only authorized clients get in: see acmd remote clients\n" +
			"and acmd remote requests. Turning it off drops every connection.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var on bool
			switch args[0] {
			case "on":
				on = true
			case "off":
			default:
				return fmt.Errorf("say on or off, not %q", args[0])
			}
			var st ipc.RemoteStatus
			if err := remoteCall(cmd, ipc.OpRemoteHosting, ipc.RemoteRef{Enabled: on}, &st); err != nil {
				return err
			}
			if asJSON {
				if err := printJSON(cmd.OutOrStdout(), st); err != nil {
					return err
				}
			} else {
				writeRemoteStatus(cmd.OutOrStdout(), st)
			}
			if on && !st.Listening {
				return &ExitError{Code: exitPortTaken, Err: fmt.Errorf("hosting can't listen: %s", cmpOr(st.Error, "port taken"))}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the status as JSON")
	return cmd
}

func newRemoteStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Hosting: whether it's on, its address and port, and its clients",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st ipc.RemoteStatus
			if err := remoteCall(cmd, ipc.OpRemoteStatus, nil, &st); err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), st)
			}
			writeRemoteStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func writeRemoteStatus(w io.Writer, st ipc.RemoteStatus) {
	switch {
	case !st.Enabled:
		fmt.Fprintln(w, "Hosting:     off (turn it on with: acmd remote hosting on)")
	case !st.Listening:
		fmt.Fprintf(w, "Hosting:     on, but not listening: %s\n", st.Error)
	case st.Port != st.ConfiguredPort:
		fmt.Fprintf(w, "Hosting:     on, listening on %s (port %d was taken)\n", st.Address, st.ConfiguredPort)
	default:
		fmt.Fprintf(w, "Hosting:     on, listening on %s\n", st.Address)
	}
	if st.Fingerprint != "" {
		fmt.Fprintf(w, "Fingerprint: %s\n", st.Fingerprint)
		fmt.Fprintf(w, "Name:        %s\n", st.Name)
	}
	fmt.Fprintf(w, "Clients:     %d authorized, %s\n", st.Authorized, plural(st.Connected, "connection"))
	if st.Pending > 0 {
		fmt.Fprintf(w, "Requests:    %d waiting for approval (acmd remote requests)\n", st.Pending)
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func newRemoteClientsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "clients",
		Short: "List the authorized clients, and who's connected",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var clients []ipc.RemoteClient
			if err := remoteCall(cmd, ipc.OpRemoteClients, nil, &clients); err != nil {
				return err
			}
			if asJSON {
				if clients == nil {
					clients = []ipc.RemoteClient{}
				}
				return printJSON(cmd.OutOrStdout(), clients)
			}
			writeRemoteClients(cmd.OutOrStdout(), clients)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func writeRemoteClients(w io.Writer, clients []ipc.RemoteClient) {
	if len(clients) == 0 {
		fmt.Fprintln(w, "No authorized clients. Add one with: acmd remote authorize <cert-file|fingerprint>")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tFINGERPRINT\tCONNECTED")
	for _, c := range clients {
		if len(c.Connections) == 0 {
			fmt.Fprintf(tw, "%s\t%s\t-\n", c.Name, c.Fingerprint)
		}
		for i, rc := range c.Connections {
			name, fp := c.Name, c.Fingerprint
			if i > 0 {
				name, fp = "", ""
			}
			conn := fmt.Sprintf("%s: from %s since %s", rc.ID, rc.Addr, rc.Since.Local().Format("2006-01-02 15:04"))
			if len(rc.Sessions) > 0 {
				conn += ", attached to " + strings.Join(rc.Sessions, ", ")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", name, fp, conn)
		}
	}
	_ = tw.Flush()
}

func newRemoteAuthorizeCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "authorize <cert-file|fingerprint>",
		Short: "Let a client connect: by its cert file, or the fingerprint it shows",
		Long: "Adds the client to [[remote.authorized_clients]] in config.toml. Get its fingerprint\n" +
			"with `acmd remote identity` on the client, or give its cert file. A client that already\n" +
			"tried to connect is also in acmd remote requests, to approve.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fp, cn, err := clientFingerprint(args[0])
			if err != nil {
				return err
			}
			if name == "" {
				name = cn
			}
			var c ipc.RemoteClient
			if err := remoteCall(cmd, ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: fp, Name: name}, &c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Authorized %q (%s): it can connect now.\n", c.Name, c.Fingerprint)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "what to call it (default: its cert's name)")
	return cmd
}

// clientFingerprint reads a client's fingerprint from its cert file, with the
// cert's name, or from the fingerprint itself.
func clientFingerprint(arg string) (fp, name string, err error) {
	if st, statErr := os.Stat(arg); statErr == nil && !st.IsDir() {
		fp, err := identity.FingerprintFromPEMFile(arg)
		if err != nil {
			return "", "", err
		}
		return fp, certName(arg), nil
	}
	fp, err = identity.ParseFingerprint(arg)
	if err != nil {
		return "", "", fmt.Errorf("%q is neither a cert file nor a fingerprint: %w", arg, err)
	}
	return fp, "", nil
}

// certName is the common name of the first cert in a PEM file ("" if none).
func certName(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a cert file the user named
	if err != nil {
		return ""
	}
	for {
		var block *pem.Block
		if block, b = pem.Decode(b); block == nil {
			return ""
		}
		if block.Type == "CERTIFICATE" {
			if c, err := x509.ParseCertificate(block.Bytes); err == nil {
				return c.Subject.CommonName
			}
			return ""
		}
	}
}

func newRemoteRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <name|fingerprint>",
		Short: "Stop a client from connecting, and drop it now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var c ipc.RemoteClient
			if err := remoteCall(cmd, ipc.OpRemoteRevoke, ipc.RemoteRef{Client: args[0]}, &c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked %q (%s): it's disconnected, and can no longer connect.\n", c.Name, c.Fingerprint)
			return nil
		},
	}
}

func newRemoteDisconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect <name|fingerprint|connection>",
		Short: "Drop a client's connections now (it stays authorized, and can reconnect)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var n ipc.RemoteDisconnected
			if err := remoteCall(cmd, ipc.OpRemoteDisconnect, ipc.RemoteRef{Client: args[0]}, &n); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Disconnected %s: closed %s.\n", args[0], plural(n.Closed, "connection"))
			return nil
		},
	}
}

func newRemoteRequestsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "requests",
		Short: "List clients waiting for approval; approve or deny them",
		Long: "A client this machine doesn't know is refused, and its request waits here for 24 hours\n" +
			"after it last tried. Approving it adds it to [[remote.authorized_clients]], and it gets\n" +
			"in the next time it connects. Nothing is ever approved by itself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var reqs []ipc.RemoteRequest
			if err := remoteCall(cmd, ipc.OpRemoteRequests, nil, &reqs); err != nil {
				return err
			}
			if asJSON {
				if reqs == nil {
					reqs = []ipc.RemoteRequest{}
				}
				return printJSON(cmd.OutOrStdout(), reqs)
			}
			writeRemoteRequests(cmd.OutOrStdout(), reqs)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	var name string
	approve := &cobra.Command{
		Use:   "approve <id>",
		Short: "Authorize a request's client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var c ipc.RemoteClient
			if err := remoteCall(cmd, ipc.OpRemoteApprove, ipc.RemoteRef{ID: args[0], Name: name}, &c); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Approved request %s: %q (%s) can connect now.\n", args[0], c.Name, c.Fingerprint)
			return nil
		},
	}
	approve.Flags().StringVar(&name, "name", "", "what to call it (default: its cert's name)")
	deny := &cobra.Command{
		Use:   "deny <id>",
		Short: "Drop a request (the client stays refused)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := remoteCall(cmd, ipc.OpRemoteDeny, ipc.RemoteRef{ID: args[0]}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Denied request %s.\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(approve, deny)
	return cmd
}

func writeRemoteRequests(w io.Writer, reqs []ipc.RemoteRequest) {
	if len(reqs) == 0 {
		fmt.Fprintln(w, "No requests waiting for approval.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tFINGERPRINT\tFROM\tLAST TRIED\tTRIES")
	for _, r := range reqs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", r.ID, r.CommonName, r.Fingerprint, r.Addr, r.LastSeen.Local().Format("2006-01-02 15:04"), r.Count)
	}
	_ = tw.Flush()
	fmt.Fprintln(w, "\nApprove one with: acmd remote requests approve <id>  (or deny <id>)")
}
