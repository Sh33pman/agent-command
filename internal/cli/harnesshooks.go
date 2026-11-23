package cli

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
)

// Each Claude harness with its own folder (CLAUDE_CONFIG_DIR) has its own
// settings.json, so acmd's hooks go in each: acmd init puts them in every
// one, and the daemon in a new one as it's added.

// claudeFolders are the Claude harnesses' own folders, in full.
func claudeFolders(hs []core.Harness, home string) []string {
	var out []string
	for _, h := range hs {
		if h.Kind == core.HarnessClaude && h.ConfigDir != "" {
			if dir := core.ExpandHome(h.ConfigDir, home); !slices.Contains(out, dir) {
				out = append(out, dir)
			}
		}
	}
	return out
}

// harnessFolders are the Claude harnesses' own folders for acmd init: the
// ones the daemon hands it when it runs init, else the running daemon's (none
// when it isn't running: the daemon adds them when it starts one).
func harnessFolders(ctx context.Context, p paths.Paths, home string) []string {
	if hs, ok := daemon.HarnessesOf(ctx); ok {
		return claudeFolders(hs, home)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.HelloFrom(version.Get().Version))
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil
	}
	defer conn.Close()
	var hs ipc.Harnesses
	if conn.Call(ipc.OpHarnesses, nil, &hs) != nil {
		return nil
	}
	var out []string
	for _, h := range hs.Harnesses {
		if h.Kind == core.HarnessClaude && h.ConfigDir != "" && h.Folder != "" && !slices.Contains(out, h.Folder) {
			out = append(out, h.Folder)
		}
	}
	return out
}

// hooksForHarnesses puts acmd's hooks in each Claude harness's folder that
// doesn't have them, as acmd init set them up (its setup.json says the
// path). Before acmd init has run, it does nothing: init does it.
func hooksForHarnesses(_ context.Context, hs []core.Harness) {
	p, err := paths.Default()
	if err != nil {
		return
	}
	rec, ok, err := setup.ReadRecord(p.Setup())
	if err != nil || !ok || rec.AcmdPath == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dirs := claudeFolders(hs, home)
	plan, err := setup.PlanHooks(setup.Input{Home: home, AcmdPath: rec.AcmdPath, HarnessDirs: dirs, Read: os.ReadFile})
	if err != nil || len(plan.Pending()) == 0 {
		return
	}
	for _, dir := range dirs {
		_ = os.MkdirAll(dir, 0o700) // a new harness's folder: Claude makes the rest
	}
	if _, err := plan.Apply(p.Backups(), home, time.Now()); err != nil {
		return
	}
	for _, dir := range dirs {
		if !slices.Contains(rec.ClaudeDirs, dir) {
			rec.ClaudeDirs = append(rec.ClaudeDirs, dir)
		}
	}
	_ = setup.WriteRecord(p.Setup(), rec)
}
