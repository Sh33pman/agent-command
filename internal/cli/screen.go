package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd screen`: what a session's terminal shows, as plain text.

func newScreenCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "screen <N|id|name-prefix>",
		Short: "Print what a session's terminal shows, as plain text",
		Long: "Prints a session's screen and recent history. --lines keeps the last lines only. Sessions on this\n" +
			"host only, for now: one added from a Remote host says its host can't send it yet.",
		Example: "  acmd screen 2\n  acmd screen api --lines 20\n  acmd screen 2 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				var screen ipc.Screen
				if err := c.call(ipc.OpSessionScreen, ipc.ScreenRequest{ID: s.ID, Lines: lines}, &screen); err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if jsonOut {
					enc := json.NewEncoder(jsonWriter(cmd))
					enc.SetIndent("", "  ")
					return enc.Encode(screen)
				}
				if screen.Text == "" {
					return nil
				}
				_, err = fmt.Fprintln(out, screen.Text)
				return err
			})
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "only the last this many lines (0: all)")
	return cmd
}
