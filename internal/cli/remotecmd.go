package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/version"
)

// `acmd remote …`: Remote sessions, where one acmd uses sessions that run on
// another.

func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Remote sessions: use sessions that run on another acmd, or share yours",
		Long: "Remote sessions let one Agent Command use sessions that run on another. The host turns\n" +
			"Hosting on and marks the sessions clients may use as Remote. A client adds the host and\n" +
			"works in its Remote sessions as if they were local. Both sides prove who they are with\n" +
			"a cert and key (mutual TLS), and each knows the other by its cert's fingerprint.\n\n" +
			"On a host: acmd remote hosting, status, clients, authorize and requests, and acmd remote on\n" +
			"<session> to make a session Remote. On a client: acmd remote add a Remote host, then\n" +
			"acmd remote browse it, and open or attach to its Remote sessions; acmd new --on <host>\n" +
			"starts one there.\n\n" +
			"This is separate from Agent Remote Control, which starts agents with --remote-control.\n\n" +
			exitCodesHelp + "\n\n" +
			"The guide, for a local network, Cloudflare Tunnel and your own certs:\n" +
			"  https://github.com/brettmostert/agent-command/blob/main/docs/remote-sessions.md",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newRemoteIdentityCmd(), newRemoteHostingCmd(), newRemoteStatusCmd(), newRemoteClientsCmd(),
		newRemoteAuthorizeCmd(), newRemoteRevokeCmd(), newRemoteDisconnectCmd(), newRemoteRequestsCmd(),
		newRemoteRenameCmd(), newRemoteCertCmd())
	cmd.AddCommand(remoteSessionCmds()...)
	cmd.AddCommand(remoteClientCmds()...)
	return cmd
}

func newRemoteIdentityCmd() *cobra.Command {
	var regenerate, yes, asJSON bool
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Show this machine's identity for Remote sessions: its fingerprint and cert",
		Long: "Prints the fingerprint other machines use to trust this one, and where its cert is.\n" +
			"acmd makes the cert and key the first time they're needed. The config can name your own\n" +
			"cert and key for Hosting ([remote.host]) or for connecting to hosts ([remote.client]);\n" +
			"when they differ, both are shown.\n\n" +
			"--regenerate makes a new identity. Every host and client that trusts this machine then\n" +
			"has to trust the new fingerprint instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			dir := p.RemoteIdentityDir()
			out := cmd.OutOrStdout()
			if regenerate && identity.Exists(dir) {
				old, err := identity.Ensure(dir)
				if err != nil {
					// A broken identity can always be replaced.
					fmt.Fprintf(cmd.ErrOrStderr(), "The current identity can't be read: %v\n", err)
				} else {
					fmt.Fprintf(out, "The current fingerprint is %s.\n", old.Fingerprint())
				}
				q := "Make a new identity? Every host and client that trusts this machine will have to trust the new fingerprint."
				if err := confirm(cmd.InOrStdin(), out, yes, q); err != nil {
					return err
				}
				if _, err := identity.Regenerate(dir); err != nil {
					return err
				}
				reloadHosting(cmd.Context(), p) // Hosting shows the new one from now on
				fmt.Fprintln(out, "Made a new identity. Give its fingerprint to your hosts and clients.")
			}
			ids, err := roleIdentities(p)
			if err != nil {
				return err
			}
			ids.address = hostingAddress(cmd.Context(), p)
			if asJSON {
				return writeIdentityJSON(out, ids)
			}
			writeIdentity(out, ids)
			return nil
		},
	}
	cmd.Flags().BoolVar(&regenerate, "regenerate", false, "make a new identity (peers must trust the new fingerprint)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

// identities are the identities this machine shows: as a host, and as a
// client. They're one and the same unless the config names other certs.
type identities struct {
	host, client *identity.Identity
	address      string // where Hosting listens now ("" when it doesn't)
}

func (ids identities) same() bool {
	return ids.host.CertPath == ids.client.CertPath && ids.host.Fingerprint() == ids.client.Fingerprint()
}

// roleIdentities resolves both roles' identities from config.toml.
func roleIdentities(p paths.Paths) (identities, error) {
	c, err := config.Load(p.Config())
	if err != nil {
		return identities{}, err
	}
	dir := p.RemoteIdentityDir()
	host, err := identity.Resolve(c.Remote.Host.Cert, c.Remote.Host.Key, dir)
	if err != nil {
		return identities{}, fmt.Errorf("the Hosting identity ([remote.host] in %s): %w", p.Config(), err)
	}
	client, err := identity.Resolve(c.Remote.Client.Cert, c.Remote.Client.Key, dir)
	if err != nil {
		return identities{}, fmt.Errorf("the client identity ([remote.client] in %s): %w", p.Config(), err)
	}
	return identities{host: host, client: client}, nil
}

// hostingAddress asks a running daemon where Hosting listens. It doesn't start
// one: no daemon means no Hosting.
func hostingAddress(parent context.Context, p paths.Paths) string {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return ""
	}
	defer conn.Close()
	var st ipc.RemoteStatus
	if conn.Call(ipc.OpRemoteStatus, nil, &st) != nil || !st.Listening {
		return ""
	}
	return st.Address
}

func writeIdentity(w io.Writer, ids identities) {
	block := func(id *identity.Identity, usedFor string) {
		fmt.Fprintf(w, "Fingerprint: %s\n", id.Fingerprint())
		fmt.Fprintf(w, "Name:        %s\n", id.CommonName)
		fmt.Fprintf(w, "Cert:        %s\n", id.CertPath)
		fmt.Fprintf(w, "Used for:    %s\n", usedFor)
	}
	if ids.same() {
		block(ids.host, "Hosting and connecting to hosts, unless the config names your own cert")
	} else {
		block(ids.host, "Hosting ([remote.host]): clients pin this fingerprint")
		fmt.Fprintln(w)
		block(ids.client, "connecting to hosts ([remote.client]): hosts authorize this fingerprint")
	}
	if ids.address != "" {
		fmt.Fprintf(w, "Hosting on:  %s\n", ids.address)
	}
}

type identityJSON struct {
	Fingerprint string        `json:"fingerprint"`
	CommonName  string        `json:"common_name"`
	Cert        string        `json:"cert"`
	Key         string        `json:"key"`
	UsedFor     []string      `json:"used_for"`
	Client      *identityJSON `json:"client,omitempty"` // when connecting to hosts uses another identity
	Address     string        `json:"address,omitempty"`
}

func toIdentityJSON(id *identity.Identity, usedFor ...string) *identityJSON {
	return &identityJSON{Fingerprint: id.Fingerprint(), CommonName: id.CommonName, Cert: id.CertPath, Key: id.KeyPath, UsedFor: usedFor}
}

func writeIdentityJSON(w io.Writer, ids identities) error {
	var v *identityJSON
	if ids.same() {
		v = toIdentityJSON(ids.host, "hosting", "client")
	} else {
		v = toIdentityJSON(ids.host, "hosting")
		v.Client = toIdentityJSON(ids.client, "client")
	}
	v.Address = ids.address
	return printJSON(w, v)
}

// reloadHosting asks a running daemon to apply [remote] again, so a new
// identity shows straight away. It doesn't start one: a daemon that starts
// later uses it anyway.
func reloadHosting(parent context.Context, p paths.Paths) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return
	}
	defer conn.Close()
	_ = conn.Call(ipc.OpRemoteReload, nil, nil)
}
