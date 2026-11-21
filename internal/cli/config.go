package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
)

// `acmd config`: the settings, through the daemon, config.toml's only writer.
// The app's Settings window uses the same ops (config.get, config.set).

// configCall runs a settings op on the daemon, starting it if needed.
func configCall(parent context.Context, op string, in any) (ipc.ConfigState, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return ipc.ConfigState{}, err
	}
	defer c.close()
	var st ipc.ConfigState
	err = c.call(op, in, &st)
	return st, err
}

// setSettings changes settings through the daemon, all or none.
func setSettings(ctx context.Context, values map[string]any) (ipc.ConfigState, error) {
	return configCall(ctx, ipc.OpConfigSet, ipc.ConfigSet{Values: values})
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change acmd's settings (config.toml)",
		Long: "The settings in config.toml, which the app's Settings window (⌘,) also changes. The daemon\n" +
			"keeps the file, and its comments: a change here or by hand applies straight away. The agents'\n" +
			"settings apply to sessions started after a change, and a new voice needs `acmd speech install`.",
		Args: cobra.NoArgs,
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "List the settings and their values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := configCall(cmd.Context(), ipc.OpConfigGet, nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(jsonWriter(cmd))
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			for _, s := range st.Settings {
				b, _ := json.Marshal(st.Values[s.Key])
				if _, err := fmt.Fprintf(out, "%s = %s\n", s.Key, b); err != nil {
					return err
				}
			}
			if st.Problem != nil {
				fmt.Fprintf(out, "\n%v: these are the last good settings, and changes wait until it's fixed.\n", st.Problem)
			}
			_, err = fmt.Fprintf(out, "\n(%s)\n", tilde(st.Path))
			return err
		},
	}
	set := &cobra.Command{
		Use:   "set <setting> <value> [<setting> <value>…]",
		Short: "Change settings, keeping the rest of config.toml as it is",
		Long: "Changes one or more settings at once: all of them, or none if one is wrong. A list is\n" +
			"a JSON array or words separated by spaces.",
		Example: "  acmd config set notify.cooldown 1m\n  acmd config set -- claude_args \"--model opus\"\n" +
			"  acmd config set notify.sound \"\"   # silent\n  acmd config set remote.host.listen all remote.host.port 7500",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 || len(args)%2 != 0 {
				return fmt.Errorf("give each setting a value: acmd config set <setting> <value> …")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			values := map[string]any{}
			var keys []string
			for i := 0; i < len(args); i += 2 {
				values[args[i]] = args[i+1]
				keys = append(keys, args[i])
			}
			st, err := setSettings(cmd.Context(), values)
			if err != nil {
				return err
			}
			for _, k := range keys {
				if k == "remote_control" { // its old name
					k = "agent_remote_control"
				}
				b, _ := json.Marshal(st.Values[k])
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", k, b); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.AddCommand(show, set, &cobra.Command{
		Use:   "path",
		Short: "Print where config.toml is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), p.Config())
			return err
		},
	})
	return cmd
}
