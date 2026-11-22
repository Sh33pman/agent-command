package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/editor"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

// `acmd edit <file[:line[:col]]>`: a file in your editor, at a line. The app runs
// it for a path clicked in a terminal.

// editLaunch starts the editor; tests swap it.
var editLaunch editor.Launcher = editor.Start

func newEditCmd() *cobra.Command {
	var in string
	var line, col int
	cmd := &cobra.Command{
		Use:   "edit <file[:line[:col]]>",
		Short: "Open a file in your editor, at a line",
		Long: "Opens a file in the editor chosen with `acmd editor set`, in the window of its folder (or its\n" +
			"git repository, as the editor's target says). A relative path is read from --in, else from here.",
		Example: "  acmd edit src/main.go:42\n  acmd edit --in ~/code/app README.md --line 10",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return openInEditor(cmd.Context(), in, args[0], line, col)
		},
	}
	f := cmd.Flags()
	f.StringVar(&in, "in", "", "the folder a relative path is in, and whose window it opens in")
	f.IntVar(&line, "line", 0, "the line to open at")
	f.IntVar(&col, "column", 0, "the column to open at (with --line)")
	return cmd
}

// resolveEditPath makes path absolute against base, expanding ~/, and takes a
// :line:col off it unless a file is really called that.
func resolveEditPath(base, path string) (string, int, int) {
	abs := func(p string) string {
		if rest, ok := cutHome(p); ok {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, rest)
			}
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		return filepath.Clean(p)
	}
	if whole := abs(path); fileExists(whole) {
		return whole, 0, 0
	}
	file, line, col := editor.SplitPosition(path)
	return abs(file), line, col
}

func cutHome(p string) (string, bool) {
	if p == "~" {
		return "", true
	}
	if len(p) > 1 && p[0] == '~' && p[1] == '/' {
		return p[2:], true
	}
	return "", false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// openInEditor opens path in the editor chosen with acmd editor set, at line
// and col (0: where the file's :line:col says, if it does), in the window of
// its folder. A relative path is read from in, else from here. The daemon
// runs it for the editor.open op.
func openInEditor(ctx context.Context, in, path string, line, col int) error {
	base := in
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		base = wd
	}
	file, l, c := resolveEditPath(base, path)
	if line > 0 {
		l, c = line, col
	}
	info, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("can't open %s: %w", file, errors.Unwrap(err))
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return err
	}
	if cfg.Editor.Preset == "" {
		return errors.New("no editor chosen yet: run acmd editor set, or choose one in the app's Settings → Editor")
	}
	dir := filepath.Dir(file)
	if info.IsDir() {
		dir = file
	}
	if in != "" {
		dir = in
	}
	pathList := loginPath()
	op := &editor.Opener{
		Editor: cfg.Editor, Env: loginEnv(p), Launch: editLaunch,
		Look:   func(n string) (string, error) { return setup.LookPathIn(n, pathList) },
		Exists: isExecutableFile,
	}
	folder := editor.Folder(dir, cfg.Editor.Target)
	if info.IsDir() {
		return op.Open(ctx, file)
	}
	return op.OpenFile(ctx, folder, file, l, c)
}
