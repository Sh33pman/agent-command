// Package repo finds the folder a new session should start in: the main
// checkout of a git repository, never one of its linked worktrees.
package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// worktreesSeg is where Claude puts the worktrees of its background
// sessions, inside the repository it was started in.
const worktreesSeg = string(filepath.Separator) + ".claude" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)

// Root returns dir, or, when dir is inside a linked git worktree, the main
// checkout of that repository. Claude's own worktrees
// (<repo>/.claude/worktrees/<name>) are resolved by path, so it works after
// the worktree is gone; any other linked worktree is resolved with git. A
// folder that isn't in a worktree is returned unchanged.
func Root(dir string) string {
	if dir == "" {
		return dir
	}
	dir = filepath.Clean(dir)
	if i := strings.Index(dir, worktreesSeg); i > 0 {
		return dir[:i]
	}
	if main, ok := gitMain(dir); ok {
		return main
	}
	return dir
}

// CleanEnv drops the GIT_* variables (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE…)
// that Git sets for hooks, so a git command runs on the folder it's given
// and not on the repository whose hook started us.
func CleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}

// IsWorktree reports whether dir is inside a linked worktree.
func IsWorktree(dir string) bool { return Root(dir) != filepath.Clean(dir) }

// gitMain asks git for the main checkout when dir is in a linked worktree.
func gitMain(dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir") // #nosec G204 -- fixed git arguments, dir is a path argument
	c.Env = CleanEnv(os.Environ())
	out, err := c.Output()
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", false
	}
	top, common := lines[0], lines[1]
	if filepath.Base(common) != ".git" { // bare repository or odd layout
		return "", false
	}
	main := filepath.Dir(common)
	if main == top {
		return "", false // already the main checkout
	}
	return main, true
}
