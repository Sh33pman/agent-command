package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

// `acmd editor …`: which editor opens session folders.

func newEditorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "editor",
		Short: "Show, detect or set the editor that opens session folders",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newEditorShowCmd(), newEditorDetectCmd(), newEditorSetCmd())
	return cmd
}

func newEditorShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the configured editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			cfg, err := config.Load(p.Config())
			if err != nil {
				return err
			}
			e, out := cfg.Editor, cmd.OutOrStdout()
			if e.Preset == "" {
				_, err := fmt.Fprintln(out, "No editor chosen yet. Choose one with: acmd editor set vscode|cursor|zed|idea, or acmd editor set custom -- <command> {dir}")
				return err
			}
			fmt.Fprintf(out, "preset:   %s\n", e.Preset)
			if len(e.Command) > 0 {
				fmt.Fprintf(out, "command:  %s\n", strings.Join(e.Command, " "))
			}
			resolved := e.Resolved
			if resolved == "" {
				resolved = "(not found; the app is opened by bundle id)"
			}
			fmt.Fprintf(out, "resolved: %s\nrun in:   %s\ntarget:   %s\n", resolved, e.RunIn, e.Target)
			return nil
		},
	}
}

func newEditorDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "List the supported editors installed on this Mac",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pathList := loginPath()
			for _, d := range setup.DetectEditors(func(n string) (string, error) { return setup.LookPathIn(n, pathList) }, hasBundle) {
				state := "not found"
				switch {
				case d.Launcher != "":
					state = "✓ " + d.Launcher
				case d.App:
					state = "✓ app installed (no `" + d.EditorPreset.Launcher + "` launcher on PATH)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-7s %-14s %s\n", d.Name, d.Label, state)
			}
			return nil
		},
	}
}

func newEditorSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set vscode|cursor|zed|idea | set custom -- <command…{dir}…>",
		Short: "Choose the editor (custom commands get the folder as {dir}, added at the end if omitted, and a file as {file}, {line} and {col})",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values := map[string]any{"editor.preset": args[0], "editor.command": []string{}, "editor.run_in": "background"}
			switch name := args[0]; name {
			case "custom":
				if len(args) < 2 {
					return errors.New("usage: acmd editor set custom -- <command> [args…] [{dir}]")
				}
				values["editor.command"] = args[1:]
			default:
				if _, ok := setup.PresetByName(name); !ok {
					return fmt.Errorf("unknown editor %q: use vscode, cursor, zed, idea or custom", name)
				}
				if len(args) > 1 {
					return fmt.Errorf("%s takes no command (that's for custom)", name)
				}
			}
			// The daemon finds it (resolveEditor), and says if it isn't installed.
			if _, err := setSettings(cmd.Context(), values); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Editor set to %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// resolveEditor finds a chosen editor's launcher on the login shell's PATH,
// for the daemon (Config.ResolveEditor). A custom command gets {dir} added
// when it has none. A preset with no launcher still works when its app is
// installed: it's opened by bundle id.
func resolveEditor(e config.Editor) (config.Editor, error) {
	pathList := loginPath()
	switch e.Preset {
	case "":
		e.Command, e.Resolved = nil, ""
	case "custom":
		if len(e.Command) == 0 {
			return e, errors.New("a custom editor needs its command, such as nvim {dir}")
		}
		e.Command = setup.WithDir(append([]string(nil), e.Command...))
		resolved, err := setup.LookPathIn(e.Command[0], pathList)
		if err != nil {
			return e, err
		}
		e.Resolved = resolved
	default:
		preset, ok := setup.PresetByName(e.Preset)
		if !ok {
			return e, fmt.Errorf("unknown editor %q: use vscode, cursor, zed, idea or custom", e.Preset)
		}
		e.Command, e.Resolved = nil, ""
		if l, err := setup.LookPathIn(preset.Launcher, pathList); err == nil {
			e.Resolved = l
		} else if !hasAnyBundle(preset) {
			return e, fmt.Errorf("%s doesn't seem to be installed (no `%s` on PATH and no app found)", preset.Label, preset.Launcher)
		}
	}
	return e, nil
}

func hasAnyBundle(p setup.EditorPreset) bool {
	for _, id := range p.Bundles {
		if hasBundle(id) {
			return true
		}
	}
	return false
}

// loginPath is the PATH of the saved login-shell environment, else this
// process's PATH.
func loginPath() string {
	if p, err := paths.Default(); err == nil {
		if b, err := os.ReadFile(p.Env()); err == nil {
			var env map[string]string
			if json.Unmarshal(b, &env) == nil && env["PATH"] != "" {
				return env["PATH"]
			}
		}
	}
	return os.Getenv("PATH")
}
