// Package cli wires up the agent-command command tree.
package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/version"
)

// NewRootCmd builds the command tree. name is the name the binary was
// invoked as ("agent-command" or the "acmd" alias) and is used in help text.
func NewRootCmd(name string, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           name,
		Short:         "Track and navigate Claude Code sessions from a terminal sidebar",
		Long:          "agent-command (acmd) tracks Claude Code sessions and shows them in a sidebar in Kitty and Ghostty.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newVersionCmd(), newDaemonCmd(), newHookCmd(),
		newListCmd(), newRenameCmd(), newDescribeCmd(), newFinishCmd(), newArchiveCmd(), newUnarchiveCmd(), newMoveCmd(),
		newGroupCmd(), newViewModeCmd(), newSortCmd(), newFocusGroupCmd(),
		newInitCmd(), newUninstallCmd(), newEditorCmd(), newDoctorCmd(),
		newViewCmd(), newNewCmd(), newGoCmd(), newResumeCmd(),
		newSidebarCmd(), newStatusbarCmd(), newHeaderCmd(), newTermCmd(), newToggleCmd(), newFocusCmd(), newNextAttentionCmd(),
		newKeysCmd(), newPickCmd(), newOpenCmd(), newAdoptCmd(), newSplitOutCmd(), newPurgeCmd())
	// Cobra provides the `completion` command (zsh, bash, fish, powershell).

	// Using acmd for the first time offers setup (SPEC §9).
	for _, c := range root.Commands() {
		switch c.Name() {
		case "list", "rename", "describe", "archive", "unarchive", "move", "group", "view-mode", "sort", "focus-group",
			"new", "go", "resume", "toggle", "focus", "next-attention", "pick", "open", "adopt", "split-out", "purge":
			c.Annotations = map[string]string{needsSetup: "yes"}
		}
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return autoInit(cmd) }
	// Plain `acmd` in a terminal turns this pane into a view (SPEC §6.4).
	root.Args = cobra.NoArgs
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if !isInteractive(cmd) {
			return cmd.Help()
		}
		return runView(cmd, "")
	}
	return root
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, build date and Go version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			if asJSON {
				b, err := info.JSON()
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), info.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

// Execute runs the command tree with the given arguments. argv0 is the
// path the binary was invoked as.
func Execute(argv0 string, args []string, stdout, stderr io.Writer) error {
	invokedAs = argv0
	root := NewRootCmd(filepath.Base(argv0), stdout, stderr)
	root.SetArgs(args)
	return root.Execute()
}
