package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/speech"
)

// speechEngine is acmd's speech engine for the configured model and voice.
func speechEngine() (*speech.Engine, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil, err
	}
	return &speech.Engine{Dir: filepath.Join(p.StateDir, "speech"), Runtime: p.RuntimeDir,
		Model: cfg.Speech.Model, Voice: cfg.Speech.Voice, Speed: cfg.Speech.Speed}, nil
}

func newSpeechCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "speech",
		Short: "Read responses aloud with a local text-to-speech model (Kokoro on MLX)",
		Long: `acmd can read sessions' responses aloud with a text-to-speech model that runs
on this Mac (Apple Silicon, MLX): Kokoro by default, with the voice set in
[speech] in config.toml. ` + "`acmd speech install`" + ` sets it up in acmd's own folder.`,
	}
	cmd.AddCommand(newSpeechInstallCmd(), newSpeechUninstallCmd(), newSpeechStatusCmd(), newSpeechSayCmd())
	return cmd
}

func newSpeechInstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set up speech: a private Python environment with mlx-audio, and the model (about 900 MB)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := speechEngine()
			if err != nil {
				return err
			}
			if _, err := speech.FindUV(); err != nil {
				return err
			}
			q := fmt.Sprintf("Set up speech in %s? It installs mlx-audio (pinned versions, about 600 MB) into a private Python 3.12 environment with uv and downloads %s with its voices (about 320 MB). Nothing else on your Mac changes.", tilde(e.Dir), e.Model)
			if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
				return err
			}
			if err := e.Install(cmd.Context(), cmd.OutOrStdout()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Speech is set up. Try it: acmd speech say \"Hello from acmd\"\n")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newSpeechUninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove speech (its Python environment and model)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := speechEngine()
			if err != nil {
				return err
			}
			if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, fmt.Sprintf("Remove %s?", tilde(e.Dir))); err != nil {
				return err
			}
			if err := e.Uninstall(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Speech removed.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newSpeechStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether speech is set up, and its model and voice",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := speechEngine()
			if err != nil {
				return err
			}
			st := e.Status()
			if st == "" {
				st = "set up"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Speech: %s\nModel:  %s\nVoice:  %s\nFolder: %s\n", st, e.Model, e.Voice, tilde(e.Dir))
			return nil
		},
	}
}

func newSpeechSayCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "say [text | -]",
		Short: "Read text aloud (Markdown: code blocks are skipped); - reads standard input",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := speechEngine()
			if err != nil {
				return err
			}
			text := "Hello from acmd."
			switch {
			case len(args) == 1 && args[0] == "-":
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = string(b)
			case len(args) == 1:
				text = args[0]
			}
			if strings.TrimSpace(text) == "" {
				return errors.New("nothing to say")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if out != "" {
				if err := e.Start(ctx); err != nil {
					return err
				}
				abs, err := filepath.Abs(out)
				if err != nil {
					return err
				}
				d, err := e.Synth(ctx, strings.Join(speech.Prepare(text, 1<<20), "\n"), abs)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%.1f s).\n", out, d.Seconds())
				return nil
			}
			return e.Say(ctx, text)
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "write a WAV file instead of playing")
	return cmd
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
