package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Harnesses: what sessions and jobs run in. Each is a kind (claude,
// antigravity) with its own name and settings, kept by the daemon.

// harnessChoice is a harness as --harness picks it.
type harnessChoice struct{ id, name, kind string }

func harnessChoices(hs []ipc.Harness) []harnessChoice {
	out := make([]harnessChoice, 0, len(hs))
	for _, h := range hs {
		out = append(out, harnessChoice{h.ID, h.Name, h.Kind})
	}
	return out
}

func remoteHarnessChoices(hs api.Harnesses) []harnessChoice {
	out := make([]harnessChoice, 0, len(hs.Harnesses))
	for _, h := range hs.Harnesses {
		out = append(out, harnessChoice{h.ID, h.Name, h.Kind})
	}
	return out
}

// pickHarness is what --harness names: a harness by name (ignoring case,
// "_" for a space) or id; else a kind (claude, antigravity) for that kind's
// default harness; or terminal. "" is the default harness.
func pickHarness(hs []harnessChoice, sel string) (id, kind string, err error) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return "", "", nil
	}
	if strings.EqualFold(sel, core.HarnessTerminal) {
		return "", core.HarnessTerminal, nil
	}
	slug := core.HarnessSlug(sel)
	for _, h := range hs {
		if h.id == sel || core.HarnessSlug(h.name) == slug {
			return h.id, h.kind, nil
		}
	}
	if k := strings.ToLower(sel); core.IsHarnessKind(k) {
		return "", k, nil
	}
	names := make([]string, 0, len(hs))
	for _, h := range hs {
		names = append(names, fmt.Sprintf("%q", h.name))
	}
	return "", "", fmt.Errorf("there's no harness %q: it's one of %s, a kind (claude, antigravity), or terminal", sel, strings.Join(names, ", "))
}

// resolveHarness is the harness sel names in a list (by name or id).
func resolveHarness(hs []ipc.Harness, sel string) (ipc.Harness, error) {
	id, _, err := pickHarness(harnessChoices(hs), sel)
	if err == nil && id == "" {
		err = fmt.Errorf("give a harness's name, not a kind: acmd harness list shows them")
	}
	if err != nil {
		return ipc.Harness{}, err
	}
	for _, h := range hs {
		if h.ID == id {
			return h, nil
		}
	}
	return ipc.Harness{}, fmt.Errorf("there's no harness %q", sel)
}

func newHarnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "List, add, rename, change and remove harnesses: what sessions and jobs run in",
		Long: `A harness is Claude or Antigravity, with its own name, program, extra arguments and,
for Claude, its own folder (CLAUDE_CONFIG_DIR), so one kind can be set up more than once:
two Claude accounts, each signed in on its own folder. Sessions and jobs keep their harness
through a rename. A terminal isn't a harness.

A name is letters, with single spaces between words, and unique ignoring case. A new Claude
harness's folder is ~/.claude-<name> (lower case, "_" for spaces) unless --folder says
otherwise; --folder "" is Claude's standard folder (~/.claude, or $CLAUDE_CONFIG_DIR).
Its first session there asks you to sign in.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newHarnessListCmd(), newHarnessAddCmd(), newHarnessRenameCmd(), newHarnessSetCmd(), newHarnessRemoveCmd())
	return cmd
}

func listHarnesses(c *client) (ipc.Harnesses, error) {
	var hs ipc.Harnesses
	err := c.call(ipc.OpHarnesses, nil, &hs)
	return hs, err
}

func newHarnessListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the harnesses, and which is the default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withDaemon(cmd, func(c *client) error {
				hs, err := listHarnesses(c)
				if err != nil {
					return err
				}
				printHarnesses(cmd.OutOrStdout(), hs)
				return nil
			})
		},
	}
}

func printHarnesses(w io.Writer, hs ipc.Harnesses) {
	if len(hs.Harnesses) == 0 {
		fmt.Fprintln(w, "No harnesses: add one with acmd harness add claude <name>.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tFOLDER\tCOMMAND\tARGS\t")
	home := homeDir()
	for _, h := range hs.Harnesses {
		name := h.Name
		if h.ID == hs.Default {
			name += " (default)"
		}
		if !h.Installed {
			name += " (not installed)"
		}
		folder := "-"
		if h.Kind == core.HarnessClaude {
			folder = setup.Tilde(h.Folder, home)
			if h.ConfigDir == "" {
				folder += " (standard)"
			}
		}
		command := h.Command
		if command == "" {
			command = core.KindCommand(h.Kind)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t\n", name, h.Title, folder, command, strings.Join(h.Args, " "))
	}
	_ = tw.Flush()
}

// harnessFlags are add's and set's: the settings a harness has.
type harnessFlags struct {
	command, folder string
	args            []string
}

func (f *harnessFlags) add(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.command, "command", "", `its program (default: claude or agy, found on your shell's PATH)`)
	fl.StringArrayVar(&f.args, "arg", nil, "an extra argument for each session it starts (repeat it; --arg= clears them)")
	fl.StringVar(&f.folder, "folder", "", `a Claude harness's folder, CLAUDE_CONFIG_DIR ("" is Claude's standard folder)`)
}

// into sets what the command line changed.
func (f *harnessFlags) into(cmd *cobra.Command, r *ipc.HarnessSave) {
	if cmd.Flags().Changed("command") {
		r.Command = &f.command
	}
	if cmd.Flags().Changed("arg") {
		args := make([]string, 0, len(f.args))
		for _, a := range f.args {
			if a != "" {
				args = append(args, a)
			}
		}
		r.Args = &args
	}
	if cmd.Flags().Changed("folder") {
		r.ConfigDir = &f.folder
	}
}

func newHarnessAddCmd() *cobra.Command {
	var f harnessFlags
	cmd := &cobra.Command{
		Use:     "add <claude|antigravity> <name>",
		Short:   "Add a harness: a kind, and a name",
		Example: "  acmd harness add claude Work\n  acmd harness add claude \"My Work\" --folder ~/work/.claude\n  acmd harness add antigravity Gemini --arg=--model --arg=gemini-3",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := ipc.HarnessSave{Kind: strings.ToLower(args[0]), Name: strings.Join(args[1:], " ")}
			f.into(cmd, &r)
			return withDaemon(cmd, func(c *client) error {
				var h ipc.Harness
				if err := c.call(ipc.OpHarnessAdd, r, &h); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s).\n", h.Name, h.Title)
				if h.Kind == core.HarnessClaude {
					fmt.Fprintf(cmd.OutOrStdout(), "Its folder is %s: its first session asks you to sign in.\n", setup.Tilde(h.Folder, homeDir()))
				}
				return nil
			})
		},
	}
	f.add(cmd)
	return cmd
}

// changeHarness runs harness.update on the harness sel names.
func changeHarness(cmd *cobra.Command, sel string, r ipc.HarnessSave, done string) error {
	return withDaemon(cmd, func(c *client) error {
		hs, err := listHarnesses(c)
		if err != nil {
			return err
		}
		old, err := resolveHarness(hs.Harnesses, sel)
		if err != nil {
			return err
		}
		r.ID = old.ID
		var h ipc.Harness
		if err := c.call(ipc.OpHarnessUpdate, r, &h); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), done+"\n", old.Name, h.Name)
		return nil
	})
}

func newHarnessRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <name> <new name>",
		Short: "Rename a harness (its sessions, jobs and folder stay with it)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return changeHarness(cmd, args[0], ipc.HarnessSave{Name: args[1]}, "Renamed %s to %s.")
		},
	}
}

func newHarnessSetCmd() *cobra.Command {
	var f harnessFlags
	cmd := &cobra.Command{
		Use:     "set <name> [--command C] [--arg A]... [--folder F]",
		Short:   "Change a harness's program, extra arguments or folder",
		Example: "  acmd harness set Work --arg=--model --arg=opus\n  acmd harness set Work --arg=\n  acmd harness set Claude --folder \"\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r ipc.HarnessSave
			f.into(cmd, &r)
			if r.Command == nil && r.Args == nil && r.ConfigDir == nil {
				return errors.New("give --command, --arg or --folder")
			}
			return changeHarness(cmd, args[0], r, "Changed %[2]s.")
		},
	}
	f.add(cmd)
	return cmd
}

func newHarnessRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a harness (not while a scheduled job uses it); its folder stays",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withDaemon(cmd, func(c *client) error {
				hs, err := listHarnesses(c)
				if err != nil {
					return err
				}
				h, err := resolveHarness(hs.Harnesses, args[0])
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Remove %s? Its sessions keep running; resuming one uses another %s harness.", h.Name, h.Title)
				if h.Kind == core.HarnessClaude {
					q += fmt.Sprintf(" Its folder, %s, stays.", setup.Tilde(h.Folder, homeDir()))
				}
				if err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), yes, q); err != nil {
					return err
				}
				if err := c.call(ipc.OpHarnessRemove, ipc.HarnessRef{ID: h.ID}, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Removed %s.\n", h.Name)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	return cmd
}
