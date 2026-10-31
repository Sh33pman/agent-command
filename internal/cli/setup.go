package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
)

// Setup commands (SPEC §9): init, uninstall, editor, and the automatic
// first run. Anything that looks outside acmd's own files is behind a
// variable so tests never touch the real machine.
var (
	invokedAs       = os.Args[0]
	executable      = os.Executable
	captureLoginEnv = setup.CaptureLoginEnv
	kittyDefaults   = runKittyDefaults
	hasBundle       = mdfindBundle
	kittyAppBinary  = "/Applications/kitty.app/Contents/MacOS/kitty"
)

type initOptions struct {
	dryRun      bool
	ask         bool // show the plan and ask before applying (automatic first run)
	refreshEnv  bool
	kitty       bool // configure Kitty even if it doesn't look installed
	interactive bool // a person is at the terminal (prompts allowed)
}

// gather collects everything planning needs.
type gathered struct {
	in    setup.Input
	paths paths.Paths
	cfg   config.Config
	path  string // PATH used to find claude and editors
	rec   setup.Record
	init  bool // setup.json exists
}

func gather(ctx context.Context, captureEnv, forceKitty bool) (*gathered, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil, err
	}
	g := &gathered{paths: p, cfg: cfg}
	if g.rec, g.init, err = setup.ReadRecord(p.Setup()); err != nil {
		return nil, err
	}

	var loginEnv setup.LoginEnv
	if b, err := os.ReadFile(p.Env()); err == nil && !captureEnv {
		if json.Unmarshal(b, &loginEnv) != nil {
			captureEnv = true // unreadable: capture again
		}
	} else {
		captureEnv = true
	}
	if captureEnv {
		if loginEnv, err = captureLoginEnv(ctx, os.Getenv("SHELL")); err != nil {
			return nil, err
		}
	}
	g.path = loginEnv["PATH"]
	if g.path == "" {
		g.path = os.Getenv("PATH")
	}

	exe, _ := executable()
	g.in = setup.Input{
		Home:       home,
		ConfigFile: p.Config(),
		EnvFile:    p.Env(),
		AcmdPath:   setup.StableBinary(invokedAs, exe, exec.LookPath),
		ClaudeDir:  setup.ClaudeConfigDir(home, os.Getenv),
		Keys:       setup.Keys(cfg.Keys),
		Read:       os.ReadFile,
	}
	if captureEnv {
		g.in.LoginEnv = loginEnv
	}
	if claude, err := setup.LookPathIn("claude", g.path); err == nil {
		g.in.ClaudePath = claude
	}

	kittyDir := setup.KittyConfigDir(home, os.Getenv)
	if g.init && g.rec.KittyDir != "" {
		kittyDir = g.rec.KittyDir
	}
	kittyBin := findKitty(g.path)
	if forceKitty || kittyBin != "" || dirExists(kittyDir) {
		g.in.KittyDir = kittyDir
		if kittyBin != "" {
			if d, err := kittyDefaults(ctx, kittyBin); err == nil {
				g.in.KittyKeys = d
			}
		}
	}
	return g, nil
}

func findKitty(pathList string) string {
	if p, err := setup.LookPathIn("kitty", pathList); err == nil {
		return p
	}
	if p, err := setup.LookPathIn(kittyAppBinary, ""); err == nil {
		return p
	}
	return ""
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func runKittyDefaults(ctx context.Context, kitty string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, kitty, "+runpy", setup.KittyDefaultsScript).Output() // #nosec G204 G702 -- the kitty binary found on PATH, fixed script
	if err != nil {
		return nil, err
	}
	return setup.ParseKittyDefaults(out), nil
}

// mdfindBundle reports whether Spotlight knows an app with the bundle id.
// The ids are constants from setup.EditorPresets.
func mdfindBundle(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "mdfind", "kMDItemCFBundleIdentifier == '"+id+"'").Output() // #nosec G204 -- constant bundle ids
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// runInit plans and (unless dry-run or declined) applies `acmd init`. It
// reports whether setup is complete afterwards.
func runInit(ctx context.Context, in io.Reader, out io.Writer, o initOptions) (bool, error) {
	g, err := gather(ctx, o.refreshEnv, o.kitty)
	if err != nil {
		return false, err
	}
	reader := bufio.NewReader(in)
	// The editor is asked for once, interactively: before planning for an
	// explicit `acmd init`, after the Y/n for the automatic first run.
	askEditor := g.cfg.Editor.Preset == "" && o.interactive && !o.dryRun
	switch {
	case askEditor && !o.ask:
		if e := pickEditor(reader, out, g.path); e != nil {
			g.in.Editor = e
		}
		askEditor = false
	case g.cfg.Editor.Preset != "" && g.cfg.Editor.Preset != "custom":
		// Re-resolve the launcher (it may have moved since the last init).
		if preset, ok := setup.PresetByName(g.cfg.Editor.Preset); ok {
			if p, err := setup.LookPathIn(preset.Launcher, g.path); err == nil && p != g.cfg.Editor.Resolved {
				e := g.cfg.Editor
				e.Resolved = p
				g.in.Editor = &e
			}
		}
	}

	plan, err := setup.PlanInit(g.in)
	if err != nil {
		return false, err
	}
	if o.dryRun {
		fmt.Fprintln(out, "acmd init would make these changes (dry run: nothing is changed):")
	} else {
		fmt.Fprintln(out, "acmd init:")
	}
	plan.Write(out, g.in.Home)
	switch {
	case askEditor:
		fmt.Fprintln(out, "\nEditor: you'll be asked which editor opens session folders.")
	case g.cfg.Editor.Preset == "" && g.in.Editor == nil:
		fmt.Fprintln(out, "\nEditor: not chosen yet (acmd asks when run in a terminal, or: acmd editor set)")
	}
	if o.dryRun {
		return g.init, nil
	}
	if o.ask && len(plan.Pending()) > 0 {
		fmt.Fprint(out, "\nSet up acmd now? [Y/n] ")
		line, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
			fmt.Fprintln(out, "Skipped; nothing was changed. Run `acmd init` any time.")
			return false, nil
		}
	}
	if askEditor {
		if e := pickEditor(reader, out, g.path); e != nil {
			g.in.Editor = e
			if plan, err = setup.PlanInit(g.in); err != nil {
				return false, err
			}
		}
	}
	if err := g.paths.Ensure(); err != nil {
		return false, err
	}
	backups, err := plan.Apply(g.paths.Backups(), g.in.Home, time.Now())
	if err != nil {
		return false, err
	}
	if err := setup.WriteRecord(g.paths.Setup(), setup.Record{
		Version: version.Get().Version, At: time.Now().UTC(), AcmdPath: g.in.AcmdPath,
		ClaudeDir: g.in.ClaudeDir, KittyDir: g.in.KittyDir, ConfigFile: g.in.ConfigFile,
	}); err != nil {
		return false, err
	}
	if len(plan.Pending()) == 0 {
		fmt.Fprintln(out, "\nAlready set up; nothing changed.")
	} else {
		fmt.Fprintln(out, "\nDone.")
	}
	if backups != "" {
		fmt.Fprintf(out, "Originals backed up to %s\n", setup.Tilde(backups, g.in.Home))
	}
	for _, a := range plan.After {
		fmt.Fprintln(out, a)
	}
	return true, nil
}

// pickEditor asks which editor opens session folders (SPEC §9.5); nil
// means skipped.
func pickEditor(in *bufio.Reader, out io.Writer, pathList string) *config.Editor {
	found := setup.DetectEditors(func(n string) (string, error) { return setup.LookPathIn(n, pathList) }, hasBundle)
	def := len(found) + 2 // skip
	fmt.Fprintln(out, "\nOpen sessions in which editor?   (change later: acmd editor set)")
	for i, d := range found {
		mark := ""
		switch {
		case d.Launcher != "":
			mark = "✓ detected"
		case d.App:
			mark = "✓ detected (app)"
		}
		if d.Found() && def == len(found)+2 {
			def = i + 1
		}
		fmt.Fprintf(out, "  %d) %-14s %-7s %s\n", i+1, d.Label, d.EditorPreset.Launcher, mark)
	}
	fmt.Fprintf(out, "  %d) Custom command…\n  %d) Skip for now\n", len(found)+1, len(found)+2)
	for range 3 {
		fmt.Fprintf(out, "Choice [%d]: ", def)
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			if err != nil { // EOF: no answer
				return nil
			}
			line = strconv.Itoa(def)
		}
		n, convErr := strconv.Atoi(line)
		switch {
		case convErr != nil || n < 1 || n > len(found)+2:
			fmt.Fprintln(out, "Please enter a number from the list.")
		case n == len(found)+2:
			return nil
		case n == len(found)+1:
			return askCustomEditor(in, out, pathList)
		default:
			d := found[n-1]
			return &config.Editor{Preset: d.Name, Resolved: d.Launcher, RunIn: "background"}
		}
	}
	return nil
}

func askCustomEditor(in *bufio.Reader, out io.Writer, pathList string) *config.Editor {
	for range 3 {
		fmt.Fprint(out, "Command ({dir} is the session folder; added at the end if omitted): ")
		line, err := in.ReadString('\n')
		if strings.TrimSpace(line) == "" && err != nil {
			return nil
		}
		argv, perr := setup.ParseCommand(line)
		if perr != nil {
			fmt.Fprintln(out, perr)
			continue
		}
		resolved, lerr := setup.LookPathIn(argv[0], pathList)
		if lerr != nil {
			fmt.Fprintln(out, lerr)
			continue
		}
		fmt.Fprint(out, "Is it a terminal editor (open it in a new tab)? [y/N] ")
		a, _ := in.ReadString('\n')
		runIn := "background"
		if a = strings.ToLower(strings.TrimSpace(a)); a == "y" || a == "yes" {
			runIn = "tab"
		}
		return &config.Editor{Preset: "custom", Command: argv, Resolved: resolved, RunIn: runIn}
	}
	return nil
}

func newInitCmd() *cobra.Command {
	var o initOptions
	var ghostty bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up Claude Code hooks, terminal shortcuts and acmd's config (safe to rerun)",
		Long: "Registers acmd's Claude Code hooks, adds Kitty's remote control and shortcuts, saves your login-shell\n" +
			"environment and asks which editor opens session folders. Every changed file is backed up first,\n" +
			"and `acmd uninstall` removes it all again. Use --dry-run to see the changes without making them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ghostty {
				return errors.New("Ghostty setup isn't available yet (it arrives with Ghostty support)") //nolint:staticcheck // ST1005: product name
			}
			o.interactive = isInteractive(cmd)
			_, err := runInit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), o)
			return err
		},
	}
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show the changes without making them")
	cmd.Flags().BoolVar(&o.refreshEnv, "refresh-env", false, "capture the login-shell environment again")
	cmd.Flags().BoolVar(&o.kitty, "kitty", false, "configure Kitty even if it isn't detected")
	cmd.Flags().BoolVar(&ghostty, "ghostty", false, "configure Ghostty (not available yet)")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	var dryRun, yes, keepData bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove acmd's hooks and terminal config, and stop the daemon",
		Long: "Removes exactly what `acmd init` added (hook entries, the Kitty include block and acmd's own Kitty file)\n" +
			"and nothing else, after backing the files up. Run it before `brew uninstall agent-command`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd, dryRun, yes, keepData)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the changes without making them")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().BoolVar(&keepData, "keep-data", true, "keep sessions, groups, settings and logs (--keep-data=false deletes them)")
	return cmd
}

func runUninstall(cmd *cobra.Command, dryRun, yes, keepData bool) error {
	out := cmd.OutOrStdout()
	p, err := paths.Default()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rec, ok, err := setup.ReadRecord(p.Setup())
	if err != nil {
		return err
	}
	in := setup.Input{Home: home, ClaudeDir: setup.ClaudeConfigDir(home, os.Getenv), Read: os.ReadFile}
	kittyDir := setup.KittyConfigDir(home, os.Getenv)
	if ok {
		in.ClaudeDir = rec.ClaudeDir
		if rec.KittyDir != "" {
			kittyDir = rec.KittyDir
		}
	}
	if dirExists(kittyDir) {
		in.KittyDir = kittyDir
	}
	plan, err := setup.PlanUninstall(in)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintln(out, "acmd uninstall would make these changes (dry run: nothing is changed):")
	} else {
		fmt.Fprintln(out, "acmd uninstall:")
	}
	plan.Write(out, home)
	fmt.Fprintln(out, "  stop      the acmd daemon (your Claude sessions keep running)")
	if !keepData {
		fmt.Fprintf(out, "  delete    %s and %s (backups are kept)\n", setup.Tilde(p.StateDir, home), setup.Tilde(p.ConfigDir, home))
	}
	if dryRun {
		return nil
	}
	if err := confirm(cmd.InOrStdin(), out, yes, "\nUninstall?"); err != nil {
		return err
	}
	backups, err := plan.Apply(p.Backups(), home, time.Now())
	if err != nil {
		return err
	}
	stopDaemon(cmd.Context(), p)
	if err := os.Remove(p.Setup()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !keepData {
		if err := deleteData(p); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "\nDone.")
	if backups != "" {
		fmt.Fprintf(out, "Originals backed up to %s\n", setup.Tilde(backups, home))
	}
	for _, a := range plan.After {
		fmt.Fprintln(out, a)
	}
	return nil
}

// stopDaemon asks a running daemon (of any version) to exit; it never
// starts one.
func stopDaemon(ctx context.Context, p paths.Paths) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	var mismatch *ipc.ErrVersionMismatch
	if err != nil && !errors.As(err, &mismatch) {
		return // not running
	}
	_ = conn.Call(ipc.OpShutdown, nil, nil)
	_ = conn.Close()
}

// deleteData removes acmd's state (except config backups) and config.
func deleteData(p paths.Paths) error {
	entries, err := os.ReadDir(p.StateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if filepath.Join(p.StateDir, e.Name()) == p.Backups() {
			continue
		}
		if err := os.RemoveAll(filepath.Join(p.StateDir, e.Name())); err != nil {
			return err
		}
	}
	return os.RemoveAll(p.ConfigDir)
}

// isInteractive reports whether both stdin and stdout are terminals.
func isInteractive(cmd *cobra.Command) bool {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isTerminal(in) {
		return false
	}
	out, ok := cmd.OutOrStdout().(*os.File)
	return ok && isTerminal(out)
}

// needsSetup marks commands that trigger the automatic first run.
const needsSetup = "acmd/needs-setup"

// autoInit runs before user-facing commands: on first use it shows what
// `acmd init` would change and asks (decision: show the plan, ask Y/n).
// Without a terminal it only prints a hint.
func autoInit(cmd *cobra.Command) error {
	marked := cmd == cmd.Root() // plain `acmd` opens a view
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[needsSetup] != "" {
			marked = true
		}
	}
	if !marked || os.Getenv("ACMD_NO_AUTO_INIT") != "" {
		return nil
	}
	p, err := paths.Default()
	if err != nil {
		return nil //nolint:nilerr // the command itself reports path problems
	}
	if _, ok, _ := setup.ReadRecord(p.Setup()); ok {
		return nil
	}
	if !isInteractive(cmd) {
		fmt.Fprintln(cmd.ErrOrStderr(), "acmd isn't set up yet: run `acmd init` (or `acmd init --dry-run` to preview the changes).")
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "acmd isn't set up yet. This is what setup changes:")
	_, err = runInit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), initOptions{ask: true, interactive: true})
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "setup failed:", err)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return nil
}
