package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
)

// `acmd remote on|off|sessions`: which sessions are Remote, so clients of
// Hosting can see and use them.

// remoteSessionCmds are `acmd remote on|off|sessions`, under newRemoteCmd.
func remoteSessionCmds() []*cobra.Command {
	return []*cobra.Command{newRemoteSetCmd(true), newRemoteSetCmd(false), newRemoteSessionsCmd()}
}

func newRemoteSetCmd(on bool) *cobra.Command {
	use, short := "on", "Make sessions Remote: clients of Hosting can see and use them"
	if !on {
		use, short = "off", "Make sessions local: clients attached to them are dropped at once"
	}
	return &cobra.Command{
		Use:   use + " <N|id|name-prefix>...",
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			err := withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				for _, sel := range args {
					s, err := resolveSession(snap, sel)
					if err != nil {
						return err
					}
					if err := c.mutate(ipc.Mutation{Kind: ipc.MutRemote, ID: s.ID, Flag: on}, nil); err != nil {
						return err
					}
					if on {
						fmt.Fprintf(out, "%s is Remote.\n", nameOf(s))
					} else {
						fmt.Fprintf(out, "%s is local.\n", nameOf(s))
					}
				}
				return nil
			})
			if err != nil || !on {
				return err
			}
			if host, err := remoteHosting(); err == nil && !host.Enabled {
				fmt.Fprintln(out, hostingOffNote)
			}
			return nil
		},
	}
}

// hostingOffNote says why a Remote session can't be reached yet.
const hostingOffNote = "Hosting is off, so no client can reach it yet: set enabled = true under [remote.host] in acmd's config."

// remoteHosting is [remote.host] from acmd's config.
func remoteHosting() (config.RemoteHost, error) {
	p, err := paths.Default()
	if err != nil {
		return config.RemoteHost{}, err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return config.RemoteHost{}, err
	}
	return cfg.Remote.Host, nil
}

func newRemoteSessionsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List the Remote sessions, whether Hosting is on, and the clients attached to each",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, err := remoteHosting()
			if err != nil {
				return err
			}
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				vs := snap.ViewState
				vs.Filter, vs.ShowArchived = "", false
				var rows []listRow
				for _, r := range listRows(snap, vs) {
					if r.Session.Remote {
						rows = append(rows, r)
					}
				}
				if asJSON {
					return writeRemoteJSON(cmd.OutOrStdout(), host, rows)
				}
				return writeRemoteTable(cmd.OutOrStdout(), host, rows)
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

// hostingLine is whether Hosting is on, and where it listens.
func hostingLine(h config.RemoteHost) string {
	if !h.Enabled {
		return "Hosting: off"
	}
	return "Hosting: on, at " + h.BindAddress()
}

// Clients attached to a session aren't known until Hosting runs: the table
// shows "-" for them.
func writeRemoteTable(w io.Writer, host config.RemoteHost, rows []listRow) error {
	fmt.Fprintln(w, hostingLine(host))
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No Remote sessions. Make one with acmd remote on <session>.")
		return err
	}
	if !host.Enabled {
		fmt.Fprintln(w, "Clients can't reach these sessions until Hosting is on.")
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTATUS\tNAME\tDIR\tCLIENTS")
	for _, r := range rows {
		num := "-"
		if r.Number > 0 {
			num = fmt.Sprint(r.Number)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t-\n", num, r.Session.Status, display(r.Session.Name, 40), filepath.Base(r.Session.Cwd))
	}
	return tw.Flush()
}

type remoteJSON struct {
	Hosting  bool            `json:"hosting"`
	Address  string          `json:"address,omitempty"` // where Hosting listens, when it's on
	Sessions []remoteSession `json:"sessions"`
}

type remoteSession struct {
	Number int    `json:"number,omitempty"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Cwd    string `json:"cwd"`
	// Clients attached to it: 0 until Hosting runs.
	Clients int `json:"clients"`
}

func writeRemoteJSON(w io.Writer, host config.RemoteHost, rows []listRow) error {
	out := remoteJSON{Hosting: host.Enabled, Sessions: []remoteSession{}}
	if host.Enabled {
		out.Address = host.BindAddress()
	}
	for _, r := range rows {
		s := r.Session
		out.Sessions = append(out.Sessions, remoteSession{Number: r.Number, ID: s.ID, Name: s.Name, Status: string(s.Status), Cwd: s.Cwd})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// remoteFlag is `acmd new`'s --remote and --no-remote: nil when neither is
// given, so [remote] new_sessions_remote decides.
func remoteFlag(remote, noRemote bool) (*bool, error) {
	switch {
	case remote && noRemote:
		return nil, errors.New("use --remote or --no-remote, not both")
	case remote || noRemote:
		return &remote, nil
	}
	return nil, nil
}
