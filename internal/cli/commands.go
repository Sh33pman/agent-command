package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/editor"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Commands that start, resume and open sessions. The sessions themselves show in
// the app.

type newOptions struct {
	group       string
	noCreate    bool
	cwd         string
	name        string
	description string
	prompt      string
	harness     string
	remote      *bool // nil: [remote] new_sessions_remote
}

func newNewCmd() *cobra.Command {
	var (
		o                newOptions
		remote, noRemote bool
	)
	cmd := &cobra.Command{
		Use:   "new [-- <first prompt>]",
		Short: "Start a new session hosted by acmd (default_harness, else Claude, unless --harness says otherwise)",
		Long: "Starts an interactive `claude` in a folder (default: this one), hosted by acmd, and lists it in the app.\n" +
			"A folder Claude hasn't trusted yet shows Claude's trust prompt in the session.\n" +
			"Without --harness it starts default_harness from acmd's config, or Claude when that isn't set.\n" +
			"--harness antigravity starts Antigravity's CLI (agy) instead, and --harness terminal a shell.\n" +
			"claude_args (or agy_args) in acmd's config are added to the agent's arguments.\n" +
			"--remote makes it a Remote session (clients of Hosting can use it), --no-remote a local one;\n" +
			"without either, [remote] new_sessions_remote in acmd's config decides.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if o.remote, err = remoteFlag(remote, noRemote); err != nil {
				return err
			}
			switch o.harness {
			case "", core.HarnessClaude, core.HarnessAntigravity, core.HarnessTerminal:
			default:
				return fmt.Errorf("--harness is claude, antigravity or terminal, not %q", o.harness)
			}
			o.prompt = strings.TrimSpace(strings.Join(args, " "))
			if o.cwd == "" {
				o.cwd, _ = os.Getwd()
			}
			s, err := startSession(cmd.Context(), o)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Started %s in %s (%s).\n", nameOf(s), setup.Tilde(s.Cwd, homeDir()), s.Ref())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.group, "group", "", "group to put it in (created if missing)")
	f.BoolVar(&o.noCreate, "no-create", false, "fail instead of creating a missing --group")
	f.StringVar(&o.cwd, "cwd", "", "folder to start in (default: the current folder)")
	f.StringVar(&o.name, "name", "", "session name (default: the agent's)")
	f.StringVar(&o.description, "description", "", "session description")
	f.StringVar(&o.harness, "harness", "", "what runs in it: claude, antigravity or terminal (default: default_harness, else claude)")
	f.BoolVar(&remote, "remote", false, "make it a Remote session (default: [remote] new_sessions_remote)")
	f.BoolVar(&noRemote, "no-remote", false, "make it a local session, not Remote")
	return cmd
}

func startSession(ctx context.Context, o newOptions) (*core.Session, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	// A broken config.toml stops here, before a session that would fail to start.
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil, err
	}
	o.harness = harnessFor(o.harness, cfg)
	dir, err := filepath.Abs(o.cwd)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s isn't a folder", dir)
	}
	var s core.Session
	err = withClient(ctx, func(c *client, snap ipc.Snapshot) error {
		req := ipc.NewRequest{Cwd: dir, Name: o.name, Description: o.description, Prompt: o.prompt, Harness: o.harness, Remote: o.remote}
		if o.group != "" {
			g, err := resolveGroup(snap, o.group)
			if err != nil && o.noCreate {
				return err
			}
			if err != nil {
				var created core.Group
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: o.group}, &created); err != nil {
					return err
				}
				g = &created
			}
			req.GroupID = g.ID
		}
		return c.conn.Call(ipc.OpNew, req, &s)
	})
	return &s, err
}

// harnessFor is what a new session runs: --harness, else default_harness, else
// Claude (the command line can't ask).
func harnessFor(flag string, cfg config.Config) string {
	return cmp.Or(flag, cfg.DefaultHarness, core.HarnessClaude)
}

// newSessionArgs are the extra arguments every session gets, which its host adds:
// claude_args for Claude, agy_args for agy (each agent's are its own flags), none
// for a terminal.
func newSessionArgs(cfg config.Config, harnessName string) []string {
	switch harnessName {
	case "", core.HarnessClaude:
		return cfg.ClaudeArgs
	case core.HarnessAntigravity:
		return cfg.AgyArgs
	}
	return nil
}

func newResumeCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "resume <N|id|name-prefix> | --all",
		Short: "Resume stopped sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) || len(args) > 1 {
				return errors.New("give one session, or --all")
			}
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				var targets []*core.Session
				if all {
					for _, s := range snap.Sessions {
						if s.Status == core.StatusPaused && s.ClaudeSessionID != "" { // --all: not finished ones
							targets = append(targets, s)
						}
					}
				} else {
					s, err := resolveSession(snap, args[0])
					if err != nil {
						return err
					}
					targets = append(targets, s)
				}
				var errs []error
				for _, s := range targets {
					if !s.Stopped() {
						fmt.Fprintf(cmd.OutOrStdout(), "%s is already running.\n", nameOf(s))
						continue
					}
					var r core.Session
					if err := c.conn.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", nameOf(s), err))
						continue
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Resumed %s (%s).\n", nameOf(&r), r.Ref())
				}
				if all && len(targets) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No paused sessions.")
				}
				return errors.Join(errs...)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "resume every paused session")
	return cmd
}

func newOpenCmd() *cobra.Command {
	var preset, target string
	cmd := &cobra.Command{
		Use:   "open <N|id|name-prefix>",
		Short: "Open a session's folder in your editor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return openSession(cmd.Context(), s, preset, target)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&preset, "editor", "", "use this editor once (vscode, cursor, zed, idea)")
	f.StringVar(&target, "target", "", "cwd or git_root (default: config)")
	return cmd
}

func openSession(ctx context.Context, s *core.Session, preset, target string) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return err
	}
	e := cfg.Editor
	if preset != "" {
		e = config.Editor{Preset: preset, RunIn: "background", Target: e.Target}
	}
	if target != "" {
		e.Target = target
	}
	if e.Preset == "" {
		return errors.New("no editor chosen yet: run acmd editor set")
	}
	if s.Cwd == "" {
		return errors.New("that session has no folder yet")
	}
	pathList := loginPath()
	op := &editor.Opener{
		Editor: e, Env: loginEnv(p), Launch: editor.Start,
		Look:   func(n string) (string, error) { return setup.LookPathIn(n, pathList) },
		Exists: isExecutableFile,
	}
	return op.Open(ctx, editor.Folder(s.Cwd, e.Target))
}

// bundleID is the app's identity, set in scripts/build-app.sh.
const bundleID = "com.brettmostert.agent-command"

// openApp brings up the app; swappable in tests.
var openApp = func(w io.Writer, newInstance bool) error {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(w, "Agent Command GUI is not available on this platform yet. Run 'acmd --help' for CLI subcommands.")
		return nil
	}
	return exec.Command("/usr/bin/open", openArgs(executablePath(), newInstance)...).Run() // #nosec G204 -- fixed program; argv only
}

func executablePath() string {
	exe, err := executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// openArgs are the arguments for `open`: the app this binary is part of, else
// the installed one.
func openArgs(exe string, newInstance bool) []string {
	var args []string
	if newInstance {
		args = append(args, "-n")
	}
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		args = append(args, exe[:i+len(".app")])
	} else {
		args = append(args, "-b", bundleID)
	}
	if newInstance {
		args = append(args, "--args", "--new-instance")
	}
	return args
}

// fallbackNotify shows a notification: notify-send on Linux when installed,
// terminal-notifier or osascript on macOS.
func fallbackNotify(no ipc.Notice) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if runtime.GOOS != "darwin" {
		if ns, err := setup.LookPathIn("notify-send", loginPath()); err == nil {
			_ = exec.CommandContext(ctx, ns, "--", no.Title, no.Body).Run() // #nosec G204 -- notify-send on the login PATH; argv only
		}
		return
	}
	if tn, err := setup.LookPathIn("terminal-notifier", loginPath()); err == nil {
		args := []string{"-title", no.Title, "-message", no.Body, "-group", no.SessionID}
		if no.Sound != "" {
			args = append(args, "-sound", no.Sound)
		}
		_ = exec.CommandContext(ctx, tn, args...).Run() // #nosec G204 -- terminal-notifier on the login PATH; argv only
		return
	}
	script := `on run argv
	if item 3 of argv is "" then
		display notification (item 2 of argv) with title (item 1 of argv)
	else
		display notification (item 2 of argv) with title (item 1 of argv) sound name (item 3 of argv)
	end if
end run`
	sound := no.Sound
	if sound == "default" {
		sound = "Glass"
	}
	_ = exec.CommandContext(ctx, "osascript", "-e", script, no.Title, no.Body, sound).Run() // #nosec G204 -- fixed script; argv only
}

func nameOf(s *core.Session) string {
	if s.Name != "" {
		return fmt.Sprintf("%q", s.Name)
	}
	return "session " + s.Ref()
}

func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
