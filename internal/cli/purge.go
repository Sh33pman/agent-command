package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd purge`: permanently delete archived sessions and run
// `claude rm` for them. Never automatic.

func newPurgeCmd() *cobra.Command {
	var olderThan string
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge [--older-than 30d]",
		Short: "Permanently delete archived sessions (and Claude's background sessions for them)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, err := parseAge(olderThan)
			if err != nil {
				return err
			}
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				cutoff := now().Add(-age)
				var ids []string
				for _, s := range snap.Sessions {
					if s.Status == core.StatusArchived && !s.ArchivedAt.After(cutoff) {
						ids = append(ids, s.ID)
					}
				}
				out := cmd.OutOrStdout()
				if len(ids) == 0 {
					_, err := fmt.Fprintln(out, "No archived sessions to purge.")
					return err
				}
				q := fmt.Sprintf("Permanently delete %d archived session(s)? This can't be undone.", len(ids))
				if err := confirm(cmd.InOrStdin(), out, yes, q); err != nil {
					return err
				}
				var n int
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutPurge, IDs: ids}, &n); err != nil {
					return err
				}
				_, err := fmt.Fprintf(out, "Purged %d session(s).\n", n)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "0d", "only sessions archived at least this long ago (e.g. 30d, 12h)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

// parseAge parses durations with a day unit ("30d") as well as Go's.
func parseAge(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("--older-than %q: use e.g. 30d or 12h", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("--older-than %q: use e.g. 30d or 12h", s)
	}
	return d, nil
}
