// Package cli wires up the agent-command command tree.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/term"
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
	root.AddCommand(newReportCmd(), newVersionCmd(), newPathsCmd(), newDaemonCmd(), newServiceCmd(), newHookCmd(),
		newListCmd(), newScreenCmd(), newRenameCmd(), newDescribeCmd(), newFinishCmd(), newArchiveCmd(), newUnarchiveCmd(), newMoveCmd(),
		newGroupCmd(), newViewModeCmd(), newSortCmd(), newFocusGroupCmd(),
		newInitCmd(), newUninstallCmd(), newEditorCmd(), newAgentRemoteControlCmd(), newRemoteControlCmd(), newConfigCmd(), newKeysCmd(), newDoctorCmd(),
		newNewCmd(), newResumeCmd(), newClientsCmd(), newEventsCmd(), newSpeechCmd(), newHostCmd(), newOpenCmd(), newEditCmd(), newPurgeCmd(), newScheduleCmd(), newHarnessCmd())
	root.AddCommand(newRemoteCmd())
	// Cobra provides the `completion` command (zsh, bash, fish, powershell).

	// Using acmd for the first time offers setup.
	for _, c := range root.Commands() {
		switch c.Name() {
		case "list", "rename", "describe", "archive", "unarchive", "move", "group", "view-mode", "sort", "focus-group",
			"new", "resume", "open", "purge", "schedule":
			c.Annotations = map[string]string{needsSetup: "yes"}
		}
	}
	// --host: the commands that are a daemon's ops run against a Remote host.
	for _, c := range root.Commands() {
		switch c.Name() {
		case "list", "screen", "rename", "describe", "finish", "archive", "unarchive", "move", "group", "view-mode", "sort",
			"focus-group", "new", "resume", "purge", "schedule", "config", "clients", "keys":
			markOnHost(c)
		}
	}
	root.PersistentFlags().StringVar(&targetHost, "host", os.Getenv("ACMD_HOST"),
		"run against this Remote host (a known host's name) instead of this machine; ACMD_HOST sets a default")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false,
		"print the answer of the op the command runs, in the daemon's wire types, and errors as JSON on stderr")
	root.Long += "\n\nCommands run against this machine's daemon, or with --host a Remote host's. Scripts should name\n" +
		"sessions by id: numbers follow the list's order, which changes.\n\n" + exitCodesHelp
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	usageArgs(root)
	root.PersistentPostRunE = func(*cobra.Command, []string) error { return finishOutput() }
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		startOutput(cmd)
		if targetHost != "" {
			if cmd.Annotations[onHost] == "" {
				return fmt.Errorf("acmd %s runs on this machine only: it doesn't take --host", cmd.Name())
			}
			return nil // the host is set up already
		}
		return autoInit(cmd)
	}
	// Plain `acmd` brings up the app, or its window if it's running.
	var newInstance bool
	root.Args = cobra.NoArgs
	root.Flags().BoolVar(&newInstance, "new-instance", false, "start another copy of the app, even if one is running")
	root.RunE = func(cmd *cobra.Command, _ []string) error { return openApp(cmd.OutOrStdout(), newInstance) }
	return root
}

// usageArgs makes wrong arguments a usage error (exit 2), on every command.
func usageArgs(c *cobra.Command) {
	if args := c.Args; args != nil {
		c.Args = func(cmd *cobra.Command, a []string) error {
			if err := args(cmd, a); err != nil {
				return usageError{err}
			}
			return nil
		}
	}
	for _, sub := range c.Commands() {
		usageArgs(sub)
	}
}

// onHost marks a command that can run against a Remote host (--host).
const onHost = "acmd.on-host"

func markOnHost(c *cobra.Command) {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[onHost] = "yes"
	for _, sub := range c.Commands() {
		markOnHost(sub)
	}
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, build date and Go version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			info.Screen = term.ScreenKind()
			if jsonOut {
				b, err := info.JSON()
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(jsonWriter(cmd), string(b))
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), info.String())
			return err
		},
	}
	return cmd
}

// Execute runs the command tree with the given arguments. argv0 is the
// path the binary was invoked as.
func Execute(argv0 string, args []string, stdout, stderr io.Writer) error {
	invokedAs = argv0
	root := NewRootCmd(filepath.Base(argv0), stdout, stderr)
	root.SetArgs(args)
	jsonOut = false
	err := root.Execute()
	if err != nil {
		// What changed before the failure (several sessions, some of them
		// refused) is still the answer.
		_ = finishOutput()
	}
	return err
}
