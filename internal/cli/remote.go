package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

// `acmd remote-control [on|off]`: whether sessions start with Remote Control.

func newRemoteControlCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remote-control [on|off]",
		Short: "Show or set whether sessions start with Remote Control (off by default)",
		Long: "With Remote Control on, sessions start with `claude --remote-control`, so you can use them\n" +
			"from claude.ai and the Claude app. It applies to sessions started or resumed after the\n" +
			"change; running sessions keep what they started with.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				cfg, err := config.Load(p.Config())
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, onOff(cfg.RemoteControl))
				return err
			}
			var on bool
			switch args[0] {
			case "on":
				on = true
			case "off":
			default:
				return fmt.Errorf("use on or off, not %q", args[0])
			}
			if err := setup.SaveSetting(p.Config(), "", "remote_control", on); err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "Remote Control is %s for sessions started or resumed from now on.\n", onOff(on))
			return err
		},
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
