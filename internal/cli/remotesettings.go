package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
)

// `acmd remote cert` and `acmd remote rename`: the settings of Remote sessions
// that `acmd config set` can't change one value at a time. The app's Settings
// window runs them too.

// reloadHosting asks a running daemon to read [remote] again, so a change
// applies now rather than at its next look at config.toml. It doesn't start
// one: a daemon that starts later reads the file anyway.
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

// certRoles are the sections that name a cert and key of your own.
var certRoles = map[string]string{"host": "remote.host", "client": "remote.client"}

func newRemoteCertCmd() *cobra.Command {
	var useDefault bool
	cmd := &cobra.Command{
		Use:   "cert host|client (<cert-file> <key-file> | --default)",
		Short: "Use your own cert and key for Hosting or for connecting to hosts, or acmd's again",
		Long: "Names the cert and key PEM files acmd shows as a host ([remote.host]) or as a client\n" +
			"([remote.client]). The two roles are set separately. The files are checked first: the\n" +
			"cert and key must belong together, and others mustn't be able to read the key.\n" +
			"--default goes back to acmd's own identity (acmd remote identity).",
		Example:   "  acmd remote cert host ~/certs/host.pem ~/certs/host-key.pem\n  acmd remote cert client --default",
		Args:      cobra.RangeArgs(1, 3),
		ValidArgs: []string{"host", "client"},
		RunE: func(cmd *cobra.Command, args []string) error {
			section, ok := certRoles[args[0]]
			if !ok {
				return fmt.Errorf("say host or client, not %q", args[0])
			}
			var cert, key string
			switch {
			case useDefault && len(args) == 1:
			case !useDefault && len(args) == 3:
				var err error
				if cert, err = filepath.Abs(args[1]); err != nil {
					return err
				}
				if key, err = filepath.Abs(args[2]); err != nil {
					return err
				}
				if _, err := identity.Load(cert, key); err != nil {
					return err
				}
			default:
				return fmt.Errorf("give a cert file and a key file, or --default")
			}
			p, err := paths.Default()
			if err != nil {
				return err
			}
			if err := setup.SaveSettings(p.Config(), section,
				config.KeyValue{Key: "cert", Value: cert}, config.KeyValue{Key: "key", Value: key}); err != nil {
				return err
			}
			reloadHosting(cmd.Context(), p)
			role := map[string]string{"host": "Hosting", "client": "Connecting to hosts"}[args[0]]
			if cert == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s uses acmd's identity.\n", role)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s uses %s.\n", role, cert)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&useDefault, "default", false, "use acmd's own identity again")
	return cmd
}

func newRemoteRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <name|fingerprint> <new-name>",
		Short: "Rename an authorized client (it stays authorized and connected)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			c, err := config.Load(p.Config())
			if err != nil {
				return err
			}
			name := args[1]
			if name == "" {
				return fmt.Errorf("the new name can't be empty")
			}
			var found *config.AuthorizedClient
			for i, ac := range c.Remote.AuthorizedClients {
				if ac.Name == args[0] || identity.Equal(ac.Fingerprint, args[0]) {
					found = &c.Remote.AuthorizedClients[i]
				}
			}
			if found == nil {
				return fmt.Errorf("no authorized client %q (acmd remote clients lists them)", args[0])
			}
			for _, ac := range c.Remote.AuthorizedClients {
				if ac.Name == name && ac.Fingerprint != found.Fingerprint {
					return fmt.Errorf("another client is already called %q", name)
				}
			}
			if _, err := setup.RenameAuthorizedClient(p.Config(), found.Fingerprint, name); err != nil {
				return err
			}
			reloadHosting(cmd.Context(), p)
			fmt.Fprintf(cmd.OutOrStdout(), "Renamed %q to %q (%s).\n", found.Name, name, found.Fingerprint)
			return nil
		},
	}
}
