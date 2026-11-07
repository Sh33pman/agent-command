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

// speechEngine is acmd's speech engine for the configured voice.
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
		Voice: cfg.Speech.Voice, Speed: cfg.Speech.Speed}, nil
}

// installQuestion asks to set up e.
func installQuestion(e *speech.Engine) string {
	q := fmt.Sprintf("Set up speech in %s? It downloads Kokoro with its voices (about 360 MB). Nothing else on your Mac changes.", tilde(e.Dir))
	if e.HasLegacy() {
		q += fmt.Sprintf(" The old speech engine there (%s) is removed.", speech.Size(e.LegacySize()))
	}
	return q
}

func newSpeechCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "speech",
		Short: "Read responses aloud with a local text-to-speech model (Kokoro on MLX)",
		Long: `acmd can read sessions' responses aloud with Kokoro, a text-to-speech model
that runs on this Mac (Apple Silicon, MLX) in acmd-speech, a helper that
ships with acmd. The voice is [speech] voice in config.toml.
` + "`acmd speech install`" + ` downloads the model into acmd's own folder (and removes the
engines older versions used); ` + "`acmd speech bench`" + ` times it.`,
	}
	cmd.AddCommand(newSpeechInstallCmd(), newSpeechUninstallCmd(), newSpeechStatusCmd(), newSpeechSayCmd(), newSpeechStopCmd(), newSpeechBenchCmd())
	return cmd
}

func newSpeechInstallCmd() *cobra.Command {
	var yes, progress bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set up speech: download the model (about 360 MB); removes older speech engines",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := speechEngine()
			if err != nil {
				return err
			}
			if _, err := speech.FindHelper(); err != nil {
				return err
			}
			if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, installQuestion(e)); err != nil {
				return err
			}
			if progress {
				// One line per percent, which the app shows as a progress bar.
				e.Progress = func(percent int) { fmt.Fprintf(cmd.OutOrStdout(), "progress %d\n", percent) }
			}
			if err := e.Install(cmd.Context(), cmd.OutOrStdout()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Speech is set up. Try it: acmd speech say \"Hello from acmd\"\n")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().BoolVar(&progress, "progress", false, "print how much has downloaded, for the app")
	return cmd
}

func newSpeechUninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove speech (the model, and older engines if they're still there)",
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
		Short: "Show whether speech is set up, and its voice",
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
			fmt.Fprintf(cmd.OutOrStdout(), "Speech: %s\nModel:  Kokoro-82M on MLX (acmd-speech)\nVoice:  %s\nFolder: %s\n", st, e.Voice, tilde(e.Dir))
			switch running, speaking := e.EngineStatus(cmd.Context()); {
			case speaking:
				fmt.Fprintln(cmd.OutOrStdout(), "Engine: running, reading aloud")
			case running:
				fmt.Fprintln(cmd.OutOrStdout(), "Engine: running")
			default:
				fmt.Fprintln(cmd.OutOrStdout(), "Engine: not running (starts when needed)")
			}
			if e.HasLegacy() {
				fmt.Fprintf(cmd.OutOrStdout(), "Old speech engine: %s in %s, no longer used (`acmd speech install` or `acmd speech uninstall` removes it)\n",
					speech.Size(e.LegacySize()), tilde(e.Dir))
			}
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

func newSpeechStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop reading aloud, whatever started it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := speechEngine()
			if err != nil {
				return err
			}
			return e.Stop(cmd.Context())
		},
	}
}

func newSpeechBenchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bench [text | -]",
		Short: "Time the speech engine on a text (nothing plays)",
		Long: `Speaks the text twice without playing it and prints how long until the first
audio, how long in all, and the real-time factor (generation time per second
of audio). If the engine isn't running yet it's started first, and its load
time is shown.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := "Hello from acmd. The build finished, and all 512 tests passed in 3.4 seconds. Shall I open a pull request for version 0.31.0?"
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
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			e, err := speechEngine()
			if err != nil {
				return err
			}
			if st := e.Status(); st != "" {
				return errors.New("speech: " + st)
			}
			out := cmd.OutOrStdout()
			for _, run := range []string{"first", "again"} {
				r, err := e.Bench(ctx, text)
				if err != nil {
					return err
				}
				load := "running"
				if r.Started {
					load = fmt.Sprintf("loaded in %.2fs", r.Ready.Seconds())
				}
				fmt.Fprintf(out, "%-5s first audio %.3fs, all %.3fs for %.2fs of audio (RTF %.3f), %s\n",
					run, r.FirstAudio.Seconds(), r.Total.Seconds(), r.Audio.Seconds(), r.RTF(), load)
			}
			return nil
		},
	}
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
