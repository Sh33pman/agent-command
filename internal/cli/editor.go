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

// `acmd editor …` (SPEC §8.6): which editor opens session folders.

func newEditorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "editor",
		Short: "Show, detect or set the editor that opens session folders",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newEditorShowCmd(), newEditorDetectCmd(), newEditorSetCmd(), newEditorPickCmd())
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
	var runIn string
	cmd := &cobra.Command{
		Use:   "set vscode|cursor|zed|idea | set custom [--run-in background|tab] -- <command…{dir}…>",
		Short: "Choose the editor (custom commands get the folder as {dir}, added at the end if omitted)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			pathList := loginPath()
			var e config.Editor
			switch name := args[0]; name {
			case "custom":
				if len(args) < 2 {
					return errors.New("usage: acmd editor set custom -- <command> [args…] [{dir}]")
				}
				if runIn != "background" && runIn != "tab" {
					return fmt.Errorf("--run-in must be background or tab, not %q", runIn)
				}
				argv := setup.WithDir(append([]string(nil), args[1:]...))
				resolved, err := setup.LookPathIn(argv[0], pathList)
				if err != nil {
					return err
				}
				e = config.Editor{Preset: "custom", Command: argv, Resolved: resolved, RunIn: runIn}
			default:
				preset, ok := setup.PresetByName(name)
				if !ok {
					return fmt.Errorf("unknown editor %q: use vscode, cursor, zed, idea or custom", name)
				}
				if len(args) > 1 {
					return fmt.Errorf("%s takes no command (that's for custom)", name)
				}
				e = config.Editor{Preset: name, RunIn: "background"}
				if l, err := setup.LookPathIn(preset.Launcher, pathList); err == nil {
					e.Resolved = l
				} else if !hasAnyBundle(preset) {
					return fmt.Errorf("%s doesn't seem to be installed (no `%s` on PATH and no app found)", preset.Label, preset.Launcher)
				}
			}
			if err := setup.SaveEditor(p.Config(), e); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Editor set to %s.\n", e.Preset)
			return err
		},
	}
	cmd.Flags().StringVar(&runIn, "run-in", "background", "custom only: background (GUI editors) or tab (terminal editors)")
	return cmd
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
