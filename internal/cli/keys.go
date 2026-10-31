package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/editor"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/keymap"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/tui"
	"github.com/brettmostert/agent-command/internal/version"
)

// M2d: the cheat sheet (`acmd keys`), the session picker (`acmd pick`)
// and opening session folders in the editor (`acmd open`, SPEC §8.6).

// ---- keys ----

// bindings is the registry for terminal with the user's keys and what
// init skipped.
func bindings(terminal string) []keymap.Binding {
	keys := config.Default().Keys
	skipped := map[string]string{}
	if p, err := paths.Default(); err == nil {
		if cfg, err := config.Load(p.Config()); err == nil {
			keys = cfg.Keys
		}
		if rec, ok, _ := setup.ReadRecord(p.Setup()); ok && rec.KittyDir != "" {
			if b, err := os.ReadFile(filepath.Join(rec.KittyDir, setup.KittyConfName)); err == nil { // #nosec G304 -- acmd's own file
				skipped = setup.SkippedBinds(string(b))
			}
		}
	}
	return keymap.Registry(keys, terminal, skipped)
}

func currentTerminal() string {
	if os.Getenv("TERM_PROGRAM") == "ghostty" && os.Getenv("KITTY_WINDOW_ID") == "" {
		return "ghostty"
	}
	return "kitty"
}

// groupsExist asks a running daemon (never starting one); unknown = true.
func groupsExist(ctx context.Context) bool {
	p, err := paths.Default()
	if err != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return true
	}
	defer conn.Close()
	var snap ipc.Snapshot
	if conn.Call(ipc.OpSnapshot, nil, &snap) != nil {
		return true
	}
	for _, g := range snap.Groups {
		if !g.IsDefault && g.ArchivedAt.IsZero() {
			return true
		}
	}
	return false
}

func newKeysCmd() *cobra.Command {
	var terminal, context, format string
	var interactive bool
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Show the keyboard shortcuts (the cheat sheet)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if terminal == "" {
				terminal = currentTerminal()
			}
			if terminal != "kitty" && terminal != "ghostty" {
				return fmt.Errorf("--terminal must be kitty or ghostty, not %q", terminal)
			}
			if context != "" && !slices.Contains(keymap.Contexts, context) {
				return fmt.Errorf("--context must be one of %s", strings.Join(keymap.Contexts, ", "))
			}
			bs := keymap.Filter(bindings(terminal), context, "", groupsExist(cmd.Context()))
			order := keymap.Ordered(context)
			if !interactive {
				return keymap.Write(cmd.OutOrStdout(), bs, order, format, terminal)
			}
			title := "Shortcuts · " + strings.ToUpper(terminal[:1]) + terminal[1:]
			err := tui.Page(cmd.Context(), tui.IO{}, title, func(q string, w int) []string {
				return keymap.Lines(keymap.Filter(bs, "", q, true), order, w)
			})
			if errors.Is(err, tui.ErrCanceled) {
				return nil
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&terminal, "terminal", "", "kitty or ghostty (default: the one you're in)")
	f.StringVar(&context, "context", "", "only one section: global, sidebar, groups or mouse")
	f.StringVar(&format, "format", "text", "text, markdown or json")
	f.BoolVar(&interactive, "interactive", false, "full-screen sheet with search (the cmd+shift+k overlay)")
	return cmd
}

// ---- pick ----

func newPickCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pick",
		Short: "Pick a session from the full list (fuzzy filter) and show it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				vs := snap.ViewState
				vs.Filter = ""
				var items []tui.Item
				sel := 0
				target, _ := targetView(snap, os.Getenv("ACMD_VIEW_ID"))
				for _, r := range listRows(snap, vs) {
					s := r.Session
					if s.Status == core.StatusArchived {
						continue
					}
					num := "  "
					if r.Number > 0 && r.Number < 10 {
						num = fmt.Sprintf("%d ", r.Number)
					}
					badge := ""
					if s.Kind == core.KindExternal {
						badge = " ↗"
					}
					detail := filepath.Base(s.Cwd)
					if r.Group != nil && !r.Group.IsDefault {
						detail = r.Group.Name + " · " + detail
					}
					if target != nil && target.Selected == s.ID {
						sel = len(items)
					}
					items = append(items, tui.Item{Label: fmt.Sprintf("%s%-8s %s%s", num, s.Status, nameOf(s), badge), Detail: detail, Value: s.ID})
				}
				if len(items) == 0 {
					return errors.New("no sessions yet: start one with acmd new")
				}
				it, err := tui.Choose(cmd.Context(), tui.IO{}, "Show session", items, sel)
				if err != nil {
					return err
				}
				var s *core.Session
				for _, x := range snap.Sessions {
					if x.ID == it.Value {
						s = x
					}
				}
				if s.Kind == core.KindExternal {
					return show(cmd.Context(), c, nil, s)
				}
				if target == nil {
					return errors.New("no view is open: run `acmd` in a pane to open one")
				}
				return show(cmd.Context(), c, target, s)
			})
			if errors.Is(err, tui.ErrCanceled) {
				return nil
			}
			return holdError(cmd, err)
		},
	}
}

// holdError keeps an error on screen for a moment when acmd runs in an
// overlay that closes as soon as it exits.
func holdError(cmd *cobra.Command, err error) error {
	if err != nil && os.Getenv("KITTY_WINDOW_ID") != "" && isInteractive(cmd) {
		fmt.Fprintln(cmd.ErrOrStderr(), "acmd:", err)
		time.Sleep(2 * time.Second)
	}
	return err
}

// ---- open in editor ----

func newOpenCmd() *cobra.Command {
	var preset, target string
	var win int
	cmd := &cobra.Command{
		Use:   "open [N|id|name-prefix]",
		Short: "Open a session's folder in your editor (default: the session in the current view)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				var s *core.Session
				var v *core.View
				if len(args) == 1 {
					var err error
					if s, err = resolveSession(snap, args[0]); err != nil {
						return err
					}
				}
				if tv, err := shortcutView(snap, win); err == nil {
					v = tv
				}
				if s == nil {
					if v == nil || v.Selected == "" {
						return errors.New("no session to open: give one (acmd open 2), or show one in a view")
					}
					for _, x := range snap.Sessions {
						if x.ID == v.Selected {
							s = x
						}
					}
				}
				if s == nil {
					return errors.New("that session no longer exists")
				}
				err := openSession(cmd.Context(), s, v, openOptions{preset: preset, target: target, interactive: isInteractive(cmd)})
				return holdError(cmd, err)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&preset, "editor", "", "use this editor once (vscode, cursor, zed, idea)")
	f.StringVar(&target, "target", "", "cwd or git_root (default: config)")
	kittyWindowFlag(cmd, &win)
	return cmd
}

type openOptions struct {
	preset, target string
	interactive    bool // a person is at this terminal (the editor picker may run here)
}

// openSession opens s's folder. Without a configured editor it asks (in
// this terminal, or in an overlay over the view when run from a shortcut).
func openSession(ctx context.Context, s *core.Session, v *core.View, o openOptions) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return err
	}
	e := cfg.Editor
	if o.preset != "" {
		e = config.Editor{Preset: o.preset, RunIn: "background", Target: e.Target}
	}
	if o.target != "" {
		e.Target = o.target
	}
	if e.Preset == "" {
		switch {
		case o.interactive:
			chosen, err := chooseEditorTUI(ctx, loginPath())
			if err != nil {
				return err
			}
			if chosen == nil {
				return errors.New("no editor chosen")
			}
			if err := setup.SaveEditor(p.Config(), *chosen); err != nil {
				return err
			}
			chosen.Target = e.Target
			e = *chosen
		case v != nil && v.Pane.Terminal == "kitty":
			return kittyAt(v.Pane.KittySocket).Overlay(ctx, v.Pane.KittyWindow, []string{acmdPath(), "open", s.ID}, nil)
		default:
			return errors.New("no editor chosen yet: run acmd editor set (or acmd editor pick)")
		}
	}
	env := loginEnviron()
	pathList := loginPath()
	op := &editor.Opener{
		Editor: e, Env: env, Launch: editorLaunch,
		Look:   func(n string) (string, error) { return setup.LookPathIn(n, pathList) },
		Exists: isExecutableFile,
	}
	if v != nil && v.Pane.Terminal == "kitty" {
		op.InTab = func(argv []string, dir string) error {
			return kittyAt(v.Pane.KittySocket).LaunchTab(ctx, v.Pane.KittyWindow, dir, argv, nil)
		}
	}
	if s.Cwd == "" {
		return errors.New("that session has no folder yet")
	}
	return op.Open(ctx, editor.Folder(s.Cwd, e.Target))
}

// editorLaunch is swappable in tests.
var editorLaunch editor.Launcher = editor.Start

func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// loginEnviron is the saved login-shell environment (else this process's),
// without CLAUDE* variables.
func loginEnviron() []string {
	env := os.Environ()
	if p, err := paths.Default(); err == nil {
		if b, err := os.ReadFile(p.Env()); err == nil {
			var saved setup.LoginEnv
			if json.Unmarshal(b, &saved) == nil && saved["PATH"] != "" {
				env = saved.Environ()
			}
		}
	}
	return procenv.Scrub(env)
}

// ---- editor picker (SPEC §9.5) ----

// chooseEditorTUI is the full-screen editor picker; nil means skipped.
var chooseEditorTUI = func(ctx context.Context, pathList string) (*config.Editor, error) {
	found := setup.DetectEditors(func(n string) (string, error) { return setup.LookPathIn(n, pathList) }, hasBundle)
	var items []tui.Item
	sel := -1
	for i, d := range found {
		state := ""
		switch {
		case d.Launcher != "":
			state = "✓ detected"
		case d.App:
			state = "✓ detected (app)"
		}
		if d.Found() && sel < 0 {
			sel = i
		}
		items = append(items, tui.Item{Label: d.Label, Detail: state, Value: d.Name})
	}
	items = append(items, tui.Item{Label: "Custom command…", Value: "custom"}, tui.Item{Label: "Skip for now", Value: "skip"})
	if sel < 0 {
		sel = len(items) - 1
	}
	it, err := tui.Choose(ctx, tui.IO{}, "Open sessions in which editor?   (change later: acmd editor set)", items, sel)
	if errors.Is(err, tui.ErrCanceled) || (err == nil && it.Value == "skip") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if it.Value != "custom" {
		for _, d := range found {
			if d.Name == it.Value {
				return &config.Editor{Preset: d.Name, Resolved: d.Launcher, RunIn: "background"}, nil
			}
		}
	}
	for {
		line, err := tui.Ask(ctx, tui.IO{}, "Command ({dir} is the session folder; added at the end if omitted):", "")
		if errors.Is(err, tui.ErrCanceled) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		argv, perr := setup.ParseCommand(line)
		if perr != nil {
			continue
		}
		resolved, lerr := setup.LookPathIn(argv[0], pathList)
		if lerr != nil {
			continue
		}
		kind, err := tui.Choose(ctx, tui.IO{}, "Where does "+filepath.Base(argv[0])+" run?", []tui.Item{
			{Label: "Its own window (GUI editor)", Value: "background"}, {Label: "A terminal tab next to the view", Value: "tab"},
		}, 0)
		if err != nil {
			return nil, nil //nolint:nilerr // canceled: skip
		}
		return &config.Editor{Preset: "custom", Command: argv, Resolved: resolved, RunIn: kind.Value}, nil
	}
}

func newEditorPickCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pick",
		Short: "Choose the editor from a list of the ones installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			if !isInteractive(cmd) {
				return errors.New("acmd editor pick needs a terminal; use acmd editor set <editor>")
			}
			e, err := chooseEditorTUI(cmd.Context(), loginPath())
			if err != nil || e == nil {
				return err
			}
			if err := setup.SaveEditor(p.Config(), *e); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Editor set to %s.\n", e.Preset)
			return err
		},
	}
}

// chooseEditor asks for the editor during init: the full-screen picker in
// a terminal, else the numbered prompt (scripts, tests).
func chooseEditor(ctx context.Context, in *bufio.Reader, inFile io.Reader, out io.Writer, pathList string) *config.Editor {
	if f, ok := inFile.(*os.File); ok && isTerminal(f) {
		if e, err := chooseEditorTUI(ctx, pathList); err == nil {
			return e
		}
	}
	return pickEditor(in, out, pathList)
}
