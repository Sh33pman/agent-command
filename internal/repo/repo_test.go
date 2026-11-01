package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRootClaudeWorktreeByPath(t *testing.T) {
	got := Root("/work/proj/.claude/worktrees/feat-x/internal/foo")
	if got != "/work/proj" {
		t.Fatalf("Root = %q", got)
	}
	if !IsWorktree("/work/proj/.claude/worktrees/feat-x") {
		t.Fatal("IsWorktree = false")
	}
}

func TestRootPlainFolderUnchanged(t *testing.T) {
	dir := t.TempDir()
	if got := Root(dir); got != dir {
		t.Fatalf("Root(%q) = %q", dir, got)
	}
	if Root("") != "" {
		t.Fatal("empty stays empty")
	}
}

func TestRootGitLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	main, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		// No inherited GIT_DIR & co: under a git hook they would point
		// these commands at the real repository.
		c.Env = append(CleanEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(main, "init", "-q")
	run(main, "commit", "-q", "--allow-empty", "-m", "x")
	other := filepath.Join(filepath.Dir(main), filepath.Base(main)+"-wt")
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	run(main, "worktree", "add", "-q", "-b", "b", other)
	otherReal, _ := filepath.EvalSymlinks(other)
	if got := Root(otherReal); got != main {
		t.Fatalf("linked worktree: Root = %q, want %q", got, main)
	}
	if got := Root(main); got != main {
		t.Fatalf("main checkout: Root = %q, want itself", got)
	}
}
