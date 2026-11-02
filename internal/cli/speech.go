package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/speech"
)

// speechEngine is acmd's speech engine for the configured engine, model
// and voice.
func speechEngine() (*speech.Engine, error) {
	return speechEngineKind("")
}

// speechEngineKind is speechEngine with the engine kind overridden ("" =
// the configured one).
func speechEngineKind(kind string) (*speech.Engine, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil, err
	}
	if kind == "" {
		kind = cfg.Speech.Engine
	}
	if kind == "" {
		kind = speech.Python
	}
	if !slices.Contains(speech.Kinds, kind) {
		return nil, fmt.Errorf("config.toml: [speech] engine = %q: want \"python\" or \"swift\"", kind)
	}
	return &speech.Engine{Dir: filepath.Join(p.StateDir, "speech"), Runtime: p.RuntimeDir, Kind: kind,
		Model: cfg.Speech.Model, Voice: cfg.Speech.Voice, Speed: cfg.Speech.Speed}, nil
}

// installQuestion asks to set up e.
func installQuestion(e *speech.Engine) string {
	if e.Kind == speech.Swift {
		return fmt.Sprintf("Set up speech (swift engine) in %s? It downloads Kokoro for Core ML with its voices and compiles it for this Mac (about 400 MB). No Python; nothing else on your Mac changes.", tilde(e.Dir))
	}
	return fmt.Sprintf("Set up speech (python engine) in %s? It installs mlx-audio (pinned versions, about 600 MB) into a private Python 3.12 environment with uv and downloads %s with its voices (about 320 MB). Nothing else on your Mac changes.", tilde(e.Dir), e.Model)
}

func newSpeechCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "speech",
		Short: "Read responses aloud with a local text-to-speech model (Kokoro)",
		Long: `acmd can read sessions' responses aloud with Kokoro, a text-to-speech model
that runs on this Mac (Apple Silicon). Two engines can run it, chosen by
[speech] engine in config.toml: "python" (MLX through mlx-audio, set up with uv)
or "swift" (Core ML through the acmd-speech helper, no Python). The voice is
[speech] voice. ` + "`acmd speech install`" + ` sets up the chosen engine in acmd's own folder;
` + "`acmd speech bench`" + ` compares the engines.`,
	}
	cmd.AddCommand(newSpeechInstallCmd(), newSpeechUninstallCmd(), newSpeechStatusCmd(), newSpeechSayCmd(), newSpeechBenchCmd())
	return cmd
}

func newSpeechInstallCmd() *cobra.Command {
	var yes bool
	var engine string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set up speech for the configured engine (python: about 900 MB; swift: about 400 MB)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := speechEngineKind(engine)
			if err != nil {
				return err
			}
			if e.Kind == speech.Python {
				if _, err := speech.FindUV(); err != nil {
					return err
				}
			} else if _, err := speech.FindHelper(); err != nil {
				return err
			}
			if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, installQuestion(e)); err != nil {
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
	cmd.Flags().StringVar(&engine, "engine", "", "set up this engine (python or swift) instead of the configured one")
	return cmd
}

func newSpeechUninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove speech (both engines' files and models)",
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
		Short: "Show whether speech is set up, and its engine, model and voice",
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
			model := e.Model
			if e.Kind == speech.Swift {
				model = "Kokoro-82M, Core ML (FluidAudio Kokoro ANE v3)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Speech: %s\nEngine: %s\nModel:  %s\nVoice:  %s\nFolder: %s\n", st, e.Name(), model, e.Voice, tilde(e.Dir))
			for _, k := range speech.Kinds {
				if k == e.Kind {
					continue
				}
				if o, err := speechEngineKind(k); err == nil && o.Status() == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Also set up: the %s engine ([speech] engine = %q)\n", k, k)
				}
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

func newSpeechBenchCmd() *cobra.Command {
	var engines []string
	cmd := &cobra.Command{
		Use:   "bench [text | -]",
		Short: "Time the speech engines on the same text (nothing plays)",
		Long: `Speaks the text with each engine that is set up, without playing it, and
prints how long until the first audio, how long in all, and the real-time
factor (generation time per second of audio). An engine that isn't running
yet is started first, and its load time is shown.`,
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
			out := cmd.OutOrStdout()
			ran := 0
			for _, k := range engines {
				e, err := speechEngineKind(k)
				if err != nil {
					return err
				}
				if st := e.Status(); st != "" {
					fmt.Fprintf(out, "%-7s %s\n", k, st)
					continue
				}
				for _, run := range []string{"first", "again"} {
					r, err := e.Bench(ctx, text)
					if err != nil {
						fmt.Fprintf(out, "%-7s error: %v\n", k, err)
						break
					}
					load := "running"
					if r.Started {
						load = fmt.Sprintf("loaded in %.2fs", r.Ready.Seconds())
					}
					fmt.Fprintf(out, "%-7s %-5s first audio %.3fs, all %.3fs for %.2fs of audio (RTF %.3f), %s\n",
						k, run, r.FirstAudio.Seconds(), r.Total.Seconds(), r.Audio.Seconds(), r.RTF(), load)
				}
				ran++
			}
			if ran == 0 {
				return errors.New("no speech engine is set up: run `acmd speech install` (--engine python or swift)")
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&engines, "engine", speech.Kinds, "engines to time")
	return cmd
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
