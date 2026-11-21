package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/paths"
)

// `acmd paths`: where acmd keeps things on this machine, so a UI or script
// asks rather than working them out.

// pathsJSON is acmd paths --json.
type pathsJSON struct {
	StateDir    string `json:"state_dir"`
	RuntimeDir  string `json:"runtime_dir"`
	ConfigDir   string `json:"config_dir"`
	Socket      string `json:"socket"`
	Config      string `json:"config"`
	DB          string `json:"db"`
	DaemonLog   string `json:"daemon_log"`
	Backups     string `json:"backups"`
	IdentityDir string `json:"identity_dir"`
}

func pathsOf(p paths.Paths) pathsJSON {
	return pathsJSON{StateDir: p.StateDir, RuntimeDir: p.RuntimeDir, ConfigDir: p.ConfigDir, Socket: p.Socket(),
		Config: p.Config(), DB: p.DB(), DaemonLog: p.DaemonLog(), Backups: p.Backups(), IdentityDir: p.RemoteIdentityDir()}
}

func newPathsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Print where acmd keeps its socket, settings, state and logs",
		Long: "Prints the folders and files acmd uses on this machine, after ACMD_STATE_DIR, ACMD_CONFIG_DIR and\n" +
			"the XDG variables. A UI or script can ask with --json rather than working them out.",
		Example: "  acmd paths\n  acmd paths --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			out := pathsOf(p)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, row := range [][2]string{{"state", out.StateDir}, {"runtime", out.RuntimeDir}, {"config dir", out.ConfigDir},
				{"socket", out.Socket}, {"config", out.Config}, {"database", out.DB}, {"daemon log", out.DaemonLog},
				{"backups", out.Backups}, {"identity", out.IdentityDir}} {
				fmt.Fprintf(w, "%s\t%s\n", row[0], row[1])
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}
