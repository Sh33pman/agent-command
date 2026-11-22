package cli

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd events`: the daemon's event stream, one JSON event per line.

func newEventsCmd() *cobra.Command {
	var types []string
	var since uint64
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Follow the daemon's changes as they happen, one JSON event per line",
		Long: "Prints a snapshot, then each entity added, updated (whole) or removed, one JSON event per line,\n" +
			"until stopped. --types picks entity types (" + fmt.Sprint(ipc.EntityTypes) + ").\n" +
			"--since resumes from an event's rev: what was missed, or a new snapshot when that's too far back.",
		Example: "  acmd events\n  acmd events --types session,inbox\n  acmd events --since 1042",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, t := range types {
				if !slices.Contains(ipc.EntityTypes, t) {
					return usageError{fmt.Errorf("no entity type %q: they're %v", t, ipc.EntityTypes)}
				}
			}
			if targetHost != "" {
				return fmt.Errorf("acmd events runs on this machine only for now: it doesn't take --host")
			}
			c, err := connect(cmd.Context())
			if err != nil {
				return err
			}
			defer c.close()
			go func() { // stopped: the stream ends
				<-cmd.Context().Done()
				c.close()
			}()
			if err := c.conn.Subscribe(ipc.SubscribeRequest{Types: types, Since: since}); err != nil {
				return err
			}
			out := jsonWriter(cmd) // always JSON: it's a stream of events
			enc := json.NewEncoder(out)
			for {
				ev, err := c.conn.NextEvent()
				if err != nil {
					if cmd.Context().Err() != nil {
						return nil
					}
					return fmt.Errorf("the stream ended: %w", err)
				}
				if err := enc.Encode(ev); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().StringSliceVar(&types, "types", nil, "only these entity types, comma-separated")
	cmd.Flags().Uint64Var(&since, "since", 0, "resume after this rev")
	return cmd
}
