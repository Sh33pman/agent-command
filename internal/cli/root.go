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
		Short:         "Manage the sessions of the Agent Command app from the command line",
		Long:          "agent-command (acmd) is the command line of the Agent Command app, which runs agents and terminals in panes.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newReportCmd(), newVersionCmd(), newDaemonCmd(), newHookCmd(),
		newListCmd(), newRenameCmd(), newDescribeCmd(), newFinishCmd(), newArchiveCmd(), newUnarchiveCmd(), newMoveCmd(),
		newGroupCmd(), newViewModeCmd(), newSortCmd(), newFocusGroupCmd(),
		newInitCmd(), newUninstallCmd(), newEditorCmd(), newRemoteControlCmd(), newDoctorCmd(),
		newNewCmd(), newResumeCmd(), newSpeechCmd(), newHostCmd(), newOpenCmd(), newPurgeCmd(), newScheduleCmd())
	// Cobra provides the `completion` command (zsh, bash, fish, powershell).

	// Using acmd for the first time offers setup.
	for _, c := range root.Commands() {
		switch c.Name() {
		case "list", "rename", "describe", "archive", "unarchive", "move", "group", "view-mode", "sort", "focus-group",
			"new", "resume", "open", "purge", "schedule":
			c.Annotations = map[string]string{needsSetup: "yes"}
		}
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return autoInit(cmd) }
	// Plain `acmd` brings up the app, or its window if it's running.
	var newInstance bool
	root.Args = cobra.NoArgs
	root.Flags().BoolVar(&newInstance, "new-instance", false, "start another copy of the app, even if one is running")
	root.RunE = func(*cobra.Command, []string) error { return openApp(newInstance) }
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
