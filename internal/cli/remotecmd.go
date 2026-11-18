package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/identity"
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
			"This is separate from Agent Remote Control, which starts agents with --remote-control.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newRemoteIdentityCmd())
	cmd.AddCommand(remoteSessionCmds()...)
	return cmd
}

func newRemoteIdentityCmd() *cobra.Command {
	var regenerate, yes, asJSON bool
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Show this machine's identity for Remote sessions: its fingerprint and cert",
		Long: "Prints the fingerprint other machines use to trust this one, and where its cert is.\n" +
			"acmd makes the cert and key the first time they're needed. The config can name your own\n" +
			"cert and key for Hosting ([remote.host]) or for connecting to hosts ([remote.client]).\n\n" +
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
			var id *identity.Identity
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
				if id, err = identity.Regenerate(dir); err != nil {
					return err
				}
				fmt.Fprintln(out, "Made a new identity. Give its fingerprint to your hosts and clients.")
			} else if id, err = identity.Ensure(dir); err != nil {
				return err
			}
			if asJSON {
				return writeIdentityJSON(out, id)
			}
			writeIdentity(out, id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&regenerate, "regenerate", false, "make a new identity (peers must trust the new fingerprint)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func writeIdentity(w io.Writer, id *identity.Identity) {
	fmt.Fprintf(w, "Fingerprint: %s\n", id.Fingerprint())
	fmt.Fprintf(w, "Name:        %s\n", id.CommonName)
	fmt.Fprintf(w, "Cert:        %s\n", id.CertPath)
	fmt.Fprintln(w, "Used for:    Hosting and connecting to hosts, unless the config names your own cert")
}

func writeIdentityJSON(w io.Writer, id *identity.Identity) error {
	b, err := json.MarshalIndent(struct {
		Fingerprint string `json:"fingerprint"`
		CommonName  string `json:"common_name"`
		Cert        string `json:"cert"`
		Key         string `json:"key"`
	}{id.Fingerprint(), id.CommonName, id.CertPath, id.KeyPath}, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}
