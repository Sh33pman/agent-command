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
	if !IsWorktreeTop(otherReal) || IsWorktreeTop(main) {
		t.Fatalf("IsWorktreeTop: linked %v, main %v", IsWorktreeTop(otherReal), IsWorktreeTop(main))
	}
}

func TestIsWorktreeTopWithoutGit(t *testing.T) {
	root := t.TempDir()
	dir := func(name, gitFile string) string {
		t.Helper()
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if gitFile != "" {
			if err := os.WriteFile(filepath.Join(d, ".git"), []byte(gitFile), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	cases := []struct {
		dir  string
		want bool
	}{
		{dir("plain", ""), false},
		{dir("linked", "gitdir: /r/.git/worktrees/linked\n"), true},
		{dir("submodule", "gitdir: ../.git/modules/submodule\n"), false},
		{"/w/proj/.claude/worktrees/feat-x", true},
		{"/w/proj/.claude", false},
	}
	for _, c := range cases {
		if got := IsWorktreeTop(c.dir); got != c.want {
			t.Errorf("IsWorktreeTop(%s) = %v, want %v", c.dir, got, c.want)
		}
	}
}
