package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd keys`: the app's keyboard shortcuts, as it registered them with the
// daemon: the defaults with your changes.

type keymap struct {
	Sections []struct {
		Title string `json:"title"`
		Keys  []struct {
			Action  string   `json:"action"`
			Title   string   `json:"title"`
			Note    string   `json:"note"`
			Keys    []string `json:"keys"`
			Changed bool     `json:"changed"`
		} `json:"keys"`
	} `json:"sections"`
	Fixed []struct {
		Section string `json:"section"`
		Keys    string `json:"keys"`
		Title   string `json:"title"`
	} `json:"fixed"`
}

func newKeysCmd() *cobra.Command {
	var markdown bool
	var from string
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "List the app's keyboard shortcuts (change them in the app: ⌘/ or Settings → Keyboard)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withDaemon(cmd, func(c *client) error {
				var m ipc.Keymap
				if err := c.call(ipc.OpKeys, ipc.KeysRequest{ClientID: from}, &m); err != nil {
					return err
				}
				if jsonOut {
					return nil // the daemon's answer, as it is
				}
				var km keymap
				if err := json.Unmarshal(m.Map, &km); err != nil {
					return fmt.Errorf("%s's shortcuts: %w", cmpOr(m.Device, m.ClientID), err)
				}
				if markdown {
					return writeKeysMarkdown(cmd.OutOrStdout(), km)
				}
				return writeKeysText(cmd.OutOrStdout(), km)
			})
		},
	}
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print the shortcuts as a Markdown table")
	cmd.Flags().StringVar(&from, "client", "", "whose shortcuts: a client's id (acmd clients); default the desktop app seen last")
	return cmd
}

func keysOf(keys []string) string {
	if len(keys) == 0 {
		return "(none)"
	}
	return strings.Join(keys, " ")
}

func writeKeysText(out io.Writer, km keymap) error {
	var b strings.Builder
	for i, s := range km.Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.Title + "\n")
		for _, k := range s.Keys {
			changed := ""
			if k.Changed {
				changed = "  (changed)"
			}
			fmt.Fprintf(&b, "  %-16s %s%s\n", keysOf(k.Keys), k.Title, changed)
		}
	}
	if len(km.Fixed) > 0 {
		b.WriteString("\nCan't be changed\n")
		for _, f := range km.Fixed {
			fmt.Fprintf(&b, "  %-16s %s (%s)\n", f.Keys, f.Title, f.Section)
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func writeKeysMarkdown(out io.Writer, km keymap) error {
	var b strings.Builder
	b.WriteString("# Agent Command keyboard shortcuts\n")
	cell := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	for _, s := range km.Sections {
		fmt.Fprintf(&b, "\n## %s\n\n| Keys | Action |\n|---|---|\n", s.Title)
		for _, k := range s.Keys {
			title := k.Title
			if k.Note != "" {
				title += " — " + k.Note
			}
			if k.Changed {
				title += " (changed)"
			}
			fmt.Fprintf(&b, "| %s | %s |\n", cell(keysOf(k.Keys)), cell(title))
		}
	}
	if len(km.Fixed) > 0 {
		b.WriteString("\n## Can't be changed\n\n| Keys | Action |\n|---|---|\n")
		for _, f := range km.Fixed {
			fmt.Fprintf(&b, "| %s | %s (%s) |\n", cell(f.Keys), cell(f.Title), cell(f.Section))
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}
