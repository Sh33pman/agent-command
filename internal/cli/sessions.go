package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// now is swappable in tests.
var now = time.Now

func newListCmd() *cobra.Command {
	var (
		all, desc                   bool
		group, status, filter, view string
		sortKey                     string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the sessions the app runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap snapshot) error {
				vs := snap.ViewState
				if all {
					vs.ShowArchived = true
				}
				if cmd.Flags().Changed("filter") {
					vs.Filter = filter
				}
				if view != "" {
					vs.Mode = view
				}
				if sortKey != "" {
					vs.Sort, vs.Desc = sortKey, desc
				}
				rows := listRows(snap, vs)
				rows = filterRows(rows, snap, group, status)
				if jsonOut {
					return writeListJSON(jsonWriter(cmd), snap, rows)
				}
				return writeListTable(cmd.OutOrStdout(), rows)
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "include archived sessions")
	f.StringVar(&group, "group", "", "only sessions in this group")
	f.StringVar(&status, "status", "", "only these statuses (comma-separated, e.g. busy,waiting)")
	f.StringVar(&filter, "filter", "", "filter text (g:<group> works)")
	f.StringVar(&view, "view", "", "groups or list (default: set with acmd view-mode)")
	f.StringVar(&sortKey, "sort", "", "created, name, activity, status or cwd (default: set with acmd sort)")
	f.BoolVar(&desc, "desc", false, "sort descending (with --sort)")
	return cmd
}

type listRow struct {
	Number  int
	Session *core.Session
	Group   *core.Group
}

// listRows lays sessions out like the sidebar and also includes sessions in
// collapsed groups (unnumbered), so `acmd list` shows everything.
func listRows(snap snapshot, vs core.ViewState) []listRow {
	numbers := map[string]int{}
	for _, r := range core.Layout(snap.Sessions, snap.Groups, vs) {
		if r.Session != nil {
			numbers[r.Session.ID] = r.Number
		}
	}
	expanded := make([]*core.Group, len(snap.Groups))
	for i, g := range snap.Groups {
		cp := *g
		cp.Collapsed = false
		expanded[i] = &cp
	}
	open := vs
	open.Collapsed = nil
	var out []listRow
	for _, r := range core.Layout(snap.Sessions, expanded, open) {
		if r.Session != nil {
			out = append(out, listRow{Number: numbers[r.Session.ID], Session: r.Session, Group: r.Group})
		}
	}
	return out
}

func filterRows(rows []listRow, snap snapshot, group, status string) []listRow {
	var wantGroup *core.Group
	if group != "" {
		wantGroup, _ = resolveGroup(snap, group)
		if wantGroup == nil {
			return nil
		}
	}
	statuses := map[string]bool{}
	for _, s := range strings.Split(status, ",") {
		if s = strings.TrimSpace(s); s != "" {
			statuses[s] = true
		}
	}
	var out []listRow
	for _, r := range rows {
		if wantGroup != nil && r.Session.GroupID != wantGroup.ID {
			continue
		}
		if len(statuses) > 0 && !statuses[string(r.Session.Status)] {
			continue
		}
		out = append(out, r)
	}
	return out
}

func writeListTable(w io.Writer, rows []listRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No sessions.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTATUS\tGROUP\tNAME\tDIR\tWHERE\tLAST")
	for _, r := range rows {
		num := "-"
		if r.Number > 0 {
			num = fmt.Sprint(r.Number)
		}
		status := string(r.Session.Status)
		if r.Session.Unseen {
			status += "•"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", num, status, r.Group.Name, display(r.Session.Name, 40),
			filepath.Base(r.Session.Cwd), where(r.Session), age(r.Session))
	}
	return tw.Flush()
}

// writeListJSON is list --json: the rows' sessions as the daemon sent them
// (internal/ipc.Session), in the list's order. Numbers aren't in it: scripts
// name sessions by id.
func writeListJSON(w io.Writer, snap snapshot, rows []listRow) error {
	byID := map[string]*ipc.Session{}
	for _, s := range snap.Snapshot.Sessions {
		byID[s.ID] = s
	}
	out := make([]*ipc.Session, 0, len(rows))
	for _, r := range rows {
		if s := byID[r.Session.ID]; s != nil {
			out = append(out, s)
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// where says where a session runs: "↗ <host>" on a Remote host.
func where(s *core.Session) string {
	if s.OnHost() {
		if s.HostGone {
			return "↗ " + s.Host + " (gone)"
		}
		return "↗ " + s.Host
	}
	if s.Kind == core.KindExternal {
		t := "unknown"
		if s.External != nil && s.External.Terminal != "" {
			t = s.External.Terminal
		}
		return t + " ↗"
	}
	if s.Kind == core.KindHosted {
		return "acmd"
	}
	return "bg"
}

func age(s *core.Session) string {
	t := s.LastEventAt
	if t.IsZero() {
		t = s.CreatedAt
	}
	if t.IsZero() {
		return "-"
	}
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// display shortens a name for the table.
func display(s string, limit int) string {
	if s == "" {
		return "(unnamed)"
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

func newRenameCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:     "rename <N|id|name-prefix>… --to <name> | rename <session> <new name>",
		Short:   "Rename sessions (several are numbered: \"api - 1\", \"api - 2\")",
		Example: "  acmd rename 2 api\n  acmd rename 1 2 3 --to api   # api - 1, api - 2, api - 3",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, name := args, to
			if to == "" { // the old way: one session, then its name
				if len(args) < 2 {
					return errors.New("give the new name: acmd rename <session> <name>, or several sessions with --to <name>")
				}
				sel, name = args[:1], strings.Join(args[1:], " ")
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				ids, err := resolveSessions(snap, sel)
				if err != nil {
					return err
				}
				var res ipc.Results
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutRename, Targets: ids, Text: name}, &res); err != nil {
					return err
				}
				return reportResults(cmd.OutOrStdout(), res, "")
			})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "the new name, for one or more sessions")
	return cmd
}

// resolveSessions finds each selector's session, once each, in order.
func resolveSessions(snap snapshot, sels []string) ([]ipc.Target, error) {
	var ids []ipc.Target
	for _, sel := range sels {
		s, err := resolveSession(snap, sel)
		if err != nil {
			return nil, err
		}
		if t := targetOf(s); !slices.Contains(ids, t) {
			ids = append(ids, t)
		}
	}
	return ids, nil
}

// targetOf names s on its host, for an op.
func targetOf(s *core.Session) ipc.Target { return ipc.Target{Host: s.Host, ID: s.ID} }

// reportResults says what didn't work, one line each, and fails if any
// didn't. done, when set, says how many did.
func reportResults(w io.Writer, res ipc.Results, done string) error {
	ok := len(res.Results) - res.Failed()
	for _, r := range res.Results {
		if !r.OK {
			fmt.Fprintf(w, "%s: %s (%s)\n", r.ID, r.Error, r.Code)
		}
	}
	if done != "" {
		fmt.Fprintf(w, done, ok)
	}
	if n := res.Failed(); n > 0 {
		return &ExitError{Code: exitPartial, Err: fmt.Errorf("%d of %d didn't change", n, len(res.Results))}
	}
	return nil
}

func newDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <N|id|name-prefix> <text>",
		Short: "Set a session's description (empty text clears it)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutDescribe, Targets: []ipc.Target{targetOf(s)}, Text: strings.Join(args[1:], " ")}, nil)
			})
		},
	}
}

func newArchiveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "archive <N|id|name-prefix>…",
		Short: "Archive sessions (ends them; an agent's conversation is kept)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				ids, err := resolveSessions(snap, args)
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Archive %d sessions?", len(ids))
				if len(ids) == 1 {
					s, _ := resolveSession(snap, args[0])
					q = fmt.Sprintf("Archive %q?", s.Name)
					if s.Managed() && !s.Stopped() {
						q = fmt.Sprintf("Archive %q? Its background session will be stopped (the conversation is kept).", s.Name)
					}
				}
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				var res ipc.Results
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutArchive, Targets: ids}, &res); err != nil {
					return err
				}
				return reportResults(cmd.OutOrStdout(), res, "")
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newFinishCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "finish <N|id|name-prefix>",
		Short: "Finish a session: its agent stops; it stays listed and showing it resumes it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Finish %s? %s stops; the conversation is kept and showing it resumes it.", nameOf(s), core.AgentName(s.HarnessKind))
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutFinish, Targets: []ipc.Target{targetOf(s)}}, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Finished %s.\n", nameOf(s))
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newUnarchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unarchive <id|name-prefix>",
		Short: "Bring an archived session back (as paused)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				snap.ViewState.ShowArchived = true
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutUnarchive, Targets: []ipc.Target{targetOf(s)}}, nil)
			})
		},
	}
}

func newMoveCmd() *cobra.Command {
	var create bool
	cmd := &cobra.Command{
		Use:   "move <N|id|name-prefix>... <group>",
		Short: "Move sessions to a group",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				groupName := args[len(args)-1]
				ids, err := resolveSessions(snap, args[:len(args)-1])
				if err != nil {
					return err
				}
				// The group is on the sessions' host: a session stays on its own.
				host := ids[0].Host
				for _, t := range ids {
					if t.Host != host {
						return usageError{fmt.Errorf("those sessions are on different hosts: move the sessions of one host at a time")}
					}
				}
				g, err := resolveGroupOn(snap, groupName, host)
				if err != nil {
					if !create {
						return fmt.Errorf("%w (use --create to create it)", err)
					}
					if g, err = createGroupOn(c, groupName, "", host); err != nil {
						return err
					}
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutMove, Targets: ids, GroupID: g.ID, Host: host}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&create, "create", false, "create the group if it doesn't exist")
	return cmd
}

func createGroup(c *client, name, color string) (*core.Group, error) {
	return createGroupOn(c, name, color, "")
}

// createGroupOn makes a group on host ("" for this machine).
func createGroupOn(c *client, name, color, host string) (*core.Group, error) {
	var g ipc.Group
	if err := c.mutate(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: name, Color: color, Host: host}, &g); err != nil {
		return nil, err
	}
	g.Host = host
	return g.Core(false), nil
}
