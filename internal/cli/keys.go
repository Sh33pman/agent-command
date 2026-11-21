package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/paths"
)

// `acmd keys`: the app's keyboard shortcuts, as it last wrote them (keys.json in
// the state folder): the defaults with your changes.

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
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "List the app's keyboard shortcuts (change them in the app: ⌘/ or Settings → Keyboard)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p.Keys())
			if errors.Is(err, fs.ErrNotExist) {
				return errors.New("the app lists its shortcuts as it starts: open Agent Command once, then try again")
			}
			if err != nil {
				return err
			}
			var km keymap
			if err := json.Unmarshal(b, &km); err != nil {
				return fmt.Errorf("%s: %w", p.Keys(), err)
			}
			switch {
			case jsonOut:
				_, err = jsonWriter(cmd).Write(b)
				return err
			case markdown:
				return writeKeysMarkdown(cmd.OutOrStdout(), km)
			}
			return writeKeysText(cmd.OutOrStdout(), km)
		},
	}
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print the shortcuts as a Markdown table")
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
