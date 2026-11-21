package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// `acmd remote cert` and `acmd remote rename`: a cert and its key, checked
// and set together, and an authorized client's name. Both go through the
// daemon.

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
			if _, err := setSettings(cmd.Context(), map[string]any{section + ".cert": cert, section + ".key": key}); err != nil {
				return err
			}
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
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			c, err := connect(ctx)
			if err != nil {
				return err
			}
			defer c.close()
			var rc ipc.RemoteClient
			if err := c.conn.Call(ipc.OpRemoteRename, ipc.RemoteRef{Client: args[0], Name: args[1]}, &rc); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Renamed %q to %q (%s).\n", args[0], rc.Name, rc.Fingerprint)
			return err
		},
	}
}
