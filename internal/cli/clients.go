package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd clients`: the apps and other clients that use this daemon, and how
// each is used now.

func newClientsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clients",
		Short: "List the apps and other clients that use the daemon, and how each is used now",
		Long: "Lists each client the daemon knows: its kind, device and id, whether it's connected, and how it's\n" +
			"used now (active, idle, background). notify.route picks which of them shows each notice.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withDaemon(cmd, func(c *client) error {
				var list []ipc.Client
				if err := c.call(ipc.OpClients, nil, &list); err != nil {
					return err
				}
				if len(list) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No clients yet: the app registers when it starts.")
					return nil
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "KIND\tDEVICE\tPRESENCE\tLAST SEEN\tCAN\tID")
				for _, cl := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", cl.Kind, cmpOr(cl.Device, "-"), cl.Presence, whenText(cl.LastSeen),
						cmpOr(strings.Join(cl.Caps, ","), "-"), cl.ID)
				}
				return tw.Flush()
			})
		},
	}
}
