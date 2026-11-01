package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
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
		all, desc, asJSON           bool
		group, status, filter, view string
		sortKey                     string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions in the sidebar's order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
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
				if asJSON {
					return writeListJSON(cmd.OutOrStdout(), rows)
				}
				return writeListTable(cmd.OutOrStdout(), rows)
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "include archived sessions")
	f.StringVar(&group, "group", "", "only sessions in this group")
	f.StringVar(&status, "status", "", "only these statuses (comma-separated, e.g. busy,waiting)")
	f.StringVar(&filter, "filter", "", "filter text (overrides the sidebar's filter; g:<group> works)")
	f.StringVar(&view, "view", "", "groups or list (default: the sidebar's view)")
	f.StringVar(&sortKey, "sort", "", "created, name, activity, status or cwd (default: the sidebar's sort)")
	f.BoolVar(&desc, "desc", false, "sort descending (with --sort)")
	f.BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

type listRow struct {
	Number  int
	Session *core.Session
	Group   *core.Group
}

// listRows lays sessions out like the sidebar and also includes sessions in
// collapsed groups (unnumbered), so `acmd list` shows everything.
func listRows(snap ipc.Snapshot, vs core.ViewState) []listRow {
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
	var out []listRow
	for _, r := range core.Layout(snap.Sessions, expanded, vs) {
		if r.Session != nil {
			out = append(out, listRow{Number: numbers[r.Session.ID], Session: r.Session, Group: r.Group})
		}
	}
	return out
}

func filterRows(rows []listRow, snap ipc.Snapshot, group, status string) []listRow {
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

type jsonRow struct {
	Number          int    `json:"number,omitempty"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Status          string `json:"status"`
	StatusDetail    string `json:"status_detail,omitempty"`
	Unseen          bool   `json:"unseen,omitempty"`
	Group           string `json:"group"`
	Cwd             string `json:"cwd"`
	Kind            string `json:"kind"`
	Where           string `json:"where"`
	BgID            string `json:"bg_id,omitempty"`
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
	LastEventAt     string `json:"last_event_at,omitempty"`
}

func writeListJSON(w io.Writer, rows []listRow) error {
	out := make([]jsonRow, 0, len(rows))
	for _, r := range rows {
		s := r.Session
		j := jsonRow{Number: r.Number, ID: s.ID, Name: s.Name, Description: s.Description, Status: string(s.Status),
			StatusDetail: s.StatusDetail, Unseen: s.Unseen, Group: r.Group.Name, Cwd: s.Cwd, Kind: string(s.Kind),
			Where: where(s), BgID: s.BgID, ClaudeSessionID: s.ClaudeSessionID}
		if !s.LastEventAt.IsZero() {
			j.LastEventAt = s.LastEventAt.UTC().Format(time.RFC3339)
		}
		out = append(out, j)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// where says where a session runs (SPEC §8.7 WHERE column).
func where(s *core.Session) string {
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
	return &cobra.Command{
		Use:   "rename <N|id|name-prefix> <new name>",
		Short: "Rename a session",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutRename, ID: s.ID, Text: strings.Join(args[1:], " ")}, nil)
			})
		},
	}
}

func newDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <N|id|name-prefix> <text>",
		Short: "Set a session's description (empty text clears it)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutDescribe, ID: s.ID, Text: strings.Join(args[1:], " ")}, nil)
			})
		},
	}
}

func newArchiveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "archive <N|id|name-prefix>",
		Short: "Archive a session (stops a background session; the conversation is kept)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Archive %q?", s.Name)
				if s.Managed() && !s.Stopped() {
					q = fmt.Sprintf("Archive %q? Its background session will be stopped (the conversation is kept).", s.Name)
				}
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil)
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
		Short: "Finish a session: Claude stops it; it stays listed and showing it resumes it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Finish %s? Claude stops it; the conversation is kept and showing it resumes it.", nameOf(s))
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutFinish, ID: s.ID}, nil); err != nil {
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
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				snap.ViewState.ShowArchived = true
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutUnarchive, ID: s.ID}, nil)
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
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				groupName := args[len(args)-1]
				var ids []string
				for _, sel := range args[:len(args)-1] {
					s, err := resolveSession(snap, sel)
					if err != nil {
						return err
					}
					ids = append(ids, s.ID)
				}
				g, err := resolveGroup(snap, groupName)
				if err != nil {
					if !create {
						return fmt.Errorf("%w (use --create to create it)", err)
					}
					if g, err = createGroup(c, groupName, ""); err != nil {
						return err
					}
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutMove, IDs: ids, GroupID: g.ID}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&create, "create", false, "create the group if it doesn't exist")
	return cmd
}

func createGroup(c *client, name, color string) (*core.Group, error) {
	var g core.Group
	if err := c.mutate(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: name, Color: color}, &g); err != nil {
		return nil, err
	}
	return &g, nil
}
