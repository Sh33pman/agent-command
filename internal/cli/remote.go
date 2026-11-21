package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd agent-remote-control [on|off]`: whether agent sessions start with
// --remote-control. `acmd remote-control` is its old name, hidden, for one
// release. (`acmd remote` is left for Remote sessions, a different feature.)

func newAgentRemoteControlCmd() *cobra.Command {
	return agentRemoteControlCmd("agent-remote-control", false)
}

// newRemoteControlCmd is the old name: it still works, and says to use the new one.
func newRemoteControlCmd() *cobra.Command {
	return agentRemoteControlCmd("remote-control", true)
}

func agentRemoteControlCmd(name string, deprecated bool) *cobra.Command {
	return &cobra.Command{
		Use:   name + " [on|off]",
		Short: "Show or set whether agent sessions start with Agent Remote Control (off by default)",
		Long: "With Agent Remote Control on, agent sessions start with --remote-control, so you can use\n" +
			"them remotely: Claude sessions from claude.ai and the Claude app, Antigravity sessions from\n" +
			"Antigravity's Remote Control in a browser. It applies to sessions started or resumed after\n" +
			"the change; running sessions keep what they started with.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		Hidden:    deprecated,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deprecated {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(),
					"Note: acmd remote-control is now acmd agent-remote-control; the old name goes in a later release."); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				st, err := configCall(cmd.Context(), ipc.OpConfigGet, nil)
				if err != nil {
					return err
				}
				on, _ := st.Values["agent_remote_control"].(bool)
				_, err = fmt.Fprintln(out, onOff(on))
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
			if _, err := setSettings(cmd.Context(), map[string]any{"agent_remote_control": on}); err != nil {
				return err
			}
			_, err := fmt.Fprintf(out, "Agent Remote Control is %s for sessions started or resumed from now on.\n", onOff(on))
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
