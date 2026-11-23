package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd inbox`: what's waiting for you, here and on your hosts, answered or
// cleared through the daemon's Inbox ops (so inbox.permission_answers holds).
// An item is its number in the list, its id here, or host:id for a host's.

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "List what's waiting for you: permission prompts, questions and finished turns",
		Long: "Lists the Inbox, here and on your hosts, in the order it came: permission prompts, questions,\n" +
			"finished turns and errors, each with its session, host and options. Answer one with acmd inbox\n" +
			"answer, or clear it with acmd inbox dismiss. An item is its number here, its id, or host:id.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap snapshot) error {
				items := inboxItems(snap)
				if ok, err := emitJSON(cmd, items); ok || err != nil {
					return err
				}
				writeInbox(cmd.OutOrStdout(), snap, items)
				return nil
			})
		},
	}
	cmd.AddCommand(newInboxAnswerCmd(), newInboxDismissCmd())
	return cmd
}

func newInboxAnswerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "answer <item> <option|text>",
		Short: "Answer an Inbox item: an option by its number or text, or a reply to a finished turn",
		Long: "Answers an Inbox item by typing into its session. A permission prompt or a question takes an\n" +
			"option, by its number (from 1) or its text. A finished turn or an error takes the reply to send.",
		Example: "  acmd inbox answer 1 1\n  acmd inbox answer 2 \"Yes, and don't ask again\"\n  acmd inbox answer studio:14 \"run the tests too\"",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				it, err := resolveInboxItem(inboxItems(snap), args[0])
				if err != nil {
					return err
				}
				a, err := inboxAnswer(it, strings.Join(args[1:], " "))
				if err != nil {
					return err
				}
				if err := c.call(ipc.OpInboxAnswer, a, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Answered %s.\n", itemWhat(snap, it))
				return nil
			})
		},
	}
}

func newInboxDismissCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dismiss <item>...",
		Short: "Clear Inbox items without answering them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				items := inboxItems(snap)
				var targets []ipc.ItemTarget
				for _, sel := range args {
					it, err := resolveInboxItem(items, sel)
					if err != nil {
						return err
					}
					targets = append(targets, ipc.ItemTarget{Host: it.Host, ID: it.ID})
				}
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutInboxDismiss, Items: targets}, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Dismissed %d.\n", len(targets))
				return nil
			})
		},
	}
}

// inboxItems are the items still waiting, in the order they came.
func inboxItems(snap snapshot) []core.InboxItem {
	out := []core.InboxItem{}
	for _, it := range snap.Inbox {
		if it.State == "" || it.State == core.InboxOpen {
			out = append(out, it)
		}
	}
	return out
}

// resolveInboxItem finds sel: a number in the list (from 1), an id here, or
// host:id.
func resolveInboxItem(items []core.InboxItem, sel string) (core.InboxItem, error) {
	sel = strings.TrimPrefix(strings.TrimSpace(sel), "#")
	if host, id, ok := strings.Cut(sel, ":"); ok {
		n, err := strconv.ParseInt(id, 10, 64)
		if err == nil {
			for _, it := range items {
				if it.Host == host && it.ID == n {
					return it, nil
				}
			}
		}
		return core.InboxItem{}, fmt.Errorf("%s has no Inbox item %q waiting: see acmd inbox", host, id)
	}
	n, err := strconv.ParseInt(sel, 10, 64)
	if err != nil {
		return core.InboxItem{}, usageError{fmt.Errorf("an item is its number in acmd inbox, its id, or host:id, not %q", sel)}
	}
	if n >= 1 && int(n) <= len(items) {
		return items[n-1], nil
	}
	for _, it := range items {
		if it.Host == "" && it.ID == n {
			return it, nil
		}
	}
	return core.InboxItem{}, fmt.Errorf("no Inbox item %s is waiting: see acmd inbox", sel)
}

// inboxAnswer is how answer answers it: an option for a prompt or a question,
// else the text as a reply.
func inboxAnswer(it core.InboxItem, answer string) (ipc.InboxAnswer, error) {
	a := ipc.InboxAnswer{Host: it.Host, ID: it.ID}
	if len(it.Options) == 0 {
		a.Text = answer
		return a, nil
	}
	if it.Multipart {
		return a, fmt.Errorf("this question has several parts: answer it in the session (acmd attach %s)", it.SessionID)
	}
	if n, err := strconv.Atoi(answer); err == nil {
		if n < 1 || n > len(it.Options) {
			return a, fmt.Errorf("pick an option from 1 to %d", len(it.Options))
		}
		a.Option = n
		return a, nil
	}
	for i, o := range it.Options {
		if strings.EqualFold(strings.TrimSpace(o), strings.TrimSpace(answer)) {
			a.Option = i + 1
			return a, nil
		}
	}
	return a, fmt.Errorf("%q isn't one of the options: %s", answer, optionList(it.Options))
}

// optionList is "1 Yes · 2 No".
func optionList(opts []string) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		parts[i] = fmt.Sprintf("%d %s", i+1, o)
	}
	return strings.Join(parts, " · ")
}

// sessionOf is the session an item is for, if it's listed.
func sessionOf(snap snapshot, it core.InboxItem) *core.Session {
	for _, s := range snap.Sessions {
		if s.ID == it.SessionID && s.Host == it.Host {
			return s
		}
	}
	return nil
}

// itemWhat names an item for a sentence: its kind and session.
func itemWhat(snap snapshot, it core.InboxItem) string {
	name := "session " + it.SessionID
	if s := sessionOf(snap, it); s != nil && s.Name != "" {
		name = strconv.Quote(s.Name)
	}
	if it.Host != "" {
		name += " on " + it.Host
	}
	return fmt.Sprintf("the %s in %s", it.Kind, name)
}

func writeInbox(w io.Writer, snap snapshot, items []core.InboxItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "Nothing is waiting.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSESSION\tHOST\tKIND\tWAITING")
	for i, it := range items {
		name := it.SessionID
		if s := sessionOf(snap, it); s != nil && s.Name != "" {
			name = s.Name
		}
		text := oneLineText(it.Text, 60)
		if len(it.Options) > 0 {
			text += "  (" + optionList(it.Options) + ")"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", i+1, name, cmpOr(it.Host, "-"), it.Kind, text)
	}
	_ = tw.Flush()
}

// oneLineText is s on one line, cut to n runes.
func oneLineText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
