package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "group", Short: "Manage session groups"}
	cmd.AddCommand(newGroupListCmd(), newGroupCreateCmd(), newGroupRenameCmd(), newGroupColorCmd(),
		newGroupDeleteCmd(), newGroupArchiveCmd(), newGroupUnarchiveCmd(),
		newGroupCollapseCmd("collapse", true), newGroupCollapseCmd("expand", false))
	return cmd
}

func newGroupListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				counts := map[string]int{}
				for _, s := range snap.Sessions {
					if s.Status != core.StatusArchived {
						counts[s.GroupID]++
					}
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "GROUP\tCOLOR\tSESSIONS\tSTATE")
				for _, g := range orderedGroups(snap.Groups) {
					if !g.ArchivedAt.IsZero() && !all {
						continue
					}
					state := ""
					switch {
					case !g.ArchivedAt.IsZero():
						state = "archived"
					case g.Collapsed:
						state = "collapsed"
					}
					fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", g.Name, g.Color, counts[g.ID], state)
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include archived groups")
	return cmd
}

// orderedGroups returns groups in display order (Default last).
func orderedGroups(groups []*core.Group) []*core.Group {
	var out, def []*core.Group
	for _, g := range groups {
		if g.IsDefault {
			def = append(def, g)
		} else {
			out = append(out, g)
		}
	}
	return append(out, def...)
}

func newGroupCreateCmd() *cobra.Command {
	var color string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, _ ipc.Snapshot) error {
				g, err := createGroup(c, args[0], color)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Created group %q (%s)\n", g.Name, g.Color)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&color, "color", "", "blue, magenta, cyan, yellow, green or red (default: next in the palette)")
	return cmd
}

func groupMutation(use, short string, nargs int, build func(g *core.Group, args []string) ipc.Mutation) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(build(g, args), nil)
			})
		},
	}
}

func newGroupRenameCmd() *cobra.Command {
	return groupMutation("rename <group> <new name>", "Rename a group", 2, func(g *core.Group, a []string) ipc.Mutation {
		return ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: a[1]}
	})
}

func newGroupColorCmd() *cobra.Command {
	return groupMutation("color <group> <color>", "Change a group's color", 2, func(g *core.Group, a []string) ipc.Mutation {
		return ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: g.ID, Color: a[1]}
	})
}

func newGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <group>",
		Short: "Delete a group; its sessions move to Default (nothing is archived)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, fmt.Sprintf("Delete group %q? Its sessions move to Default.", g.Name)); err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID}, nil)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newGroupArchiveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "archive <group>",
		Short: "Archive a group and all its sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				if g.IsDefault {
					return errors.New("the Default group can't be archived")
				}
				n, running := 0, 0
				for _, s := range snap.Sessions {
					if s.GroupID == g.ID && s.Status != core.StatusArchived {
						n++
						if s.Kind == core.KindBackground && !s.Stopped() {
							running++
						}
					}
				}
				q := fmt.Sprintf("Archive group %q and its %d session(s)? %d running background session(s) will be stopped (conversations are kept).", g.Name, n, running)
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID}, nil)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newGroupUnarchiveCmd() *cobra.Command {
	var sessions bool
	cmd := &cobra.Command{
		Use:   "unarchive <group>",
		Short: "Bring an archived group back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				var g *core.Group
				for _, x := range snap.Groups {
					if !x.ArchivedAt.IsZero() && (x.ID == args[0] || strings.EqualFold(x.Name, args[0])) {
						g = x
					}
				}
				if g == nil {
					return fmt.Errorf("no archived group named %q", args[0])
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: g.ID, Flag: sessions}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&sessions, "sessions", false, "also unarchive the group's sessions (as paused)")
	return cmd
}

func newGroupCollapseCmd(use string, collapsed bool) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   use + " <group>|--all",
		Short: map[bool]string{true: "Collapse a group (or all)", false: "Expand a group (or all)"}[collapsed],
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return errors.New("give a group name or --all")
			}
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				id := ""
				if !all {
					g, err := resolveGroup(snap, args[0])
					if err != nil {
						return err
					}
					id = g.ID
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: id, Flag: collapsed}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "all groups")
	return cmd
}

func newViewModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "view-mode groups|list",
		Short:     "Switch the sidebar between group view and list view",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"groups", "list"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, _ ipc.Snapshot) error {
				return c.mutate(ipc.Mutation{Kind: ipc.MutViewMode, Text: args[0]}, nil)
			})
		},
	}
}

func newSortCmd() *cobra.Command {
	var desc bool
	cmd := &cobra.Command{
		Use:       "sort created|name|activity|status|cwd",
		Short:     "Set the sort order (shared by all sidebars)",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"created", "name", "activity", "status", "cwd"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, _ ipc.Snapshot) error {
				return c.mutate(ipc.Mutation{Kind: ipc.MutViewSort, Text: args[0], Flag: desc}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&desc, "desc", false, "descending")
	return cmd
}

func newFocusGroupCmd() *cobra.Command {
	var leave bool
	cmd := &cobra.Command{
		Use:   "focus-group <group>|--clear",
		Short: "Expand one group and collapse the others (or leave focus mode)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if leave == (len(args) == 1) {
				return errors.New("give a group name or --clear")
			}
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				id := ""
				if !leave {
					g, err := resolveGroup(snap, args[0])
					if err != nil {
						return err
					}
					id = g.ID
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutViewFocus, GroupID: id}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&leave, "clear", false, "leave focus mode, restoring the previous collapse state")
	return cmd
}
