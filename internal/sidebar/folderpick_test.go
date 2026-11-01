package sidebar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
)

func TestFolderPickerLaunchFolderFirstAndNoWorktrees(t *testing.T) {
	h := newHarness(t, listSnap(), "a")
	// The shown session works in one of Claude's worktrees.
	h.f.snap.Sessions[0].Cwd = "/w/proj/.claude/worktrees/feat-x"
	h.send(snapMsg{snap: h.f.snap, view: &core.View{ID: "v1", Selected: h.f.snap.Sessions[0].ID}, rev: 9})
	h.m.SetLaunchDir("/w/launch")

	h.keys("n")
	h.wantScreen("Start in:", "⌂ /w/launch", "● /w/proj", "New folder…")
	if strings.Contains(h.screen(), ".claude/worktrees") {
		t.Errorf("a worktree is offered:\n%s", h.screen())
	}
	h.keys("enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default /w/launch" {
		t.Errorf("default folder: %s", got)
	}

	// Down to the shown session's repository: the root, not the worktree.
	h.keys("n", "down", "enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default /w/proj" {
		t.Errorf("selected folder: %s", got)
	}
}

func TestFolderPickerLaunchDirIsRepoRoot(t *testing.T) {
	h := newHarness(t, listSnap(), "a")
	h.m.SetLaunchDir("/w/proj/.claude/worktrees/feat-x")
	if h.m.launchDir != "/w/proj" {
		t.Errorf("launch dir %q", h.m.launchDir)
	}
}

func TestFolderPickerTypedPath(t *testing.T) {
	h := newHarness(t, listSnap(), "a")
	h.m.SetLaunchDir("/w/launch")
	h.keys("n")
	h.typeText("~/code/x")
	h.wantScreen("… ~/code/x")
	h.keys("enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default /home/code/x" {
		t.Errorf("typed path: %s", got)
	}
}

// openNewFolder opens the picker and moves onto "New folder…" (the last
// item) and chooses it.
func openNewFolder(h *harness) {
	h.keys("n")
	for range 20 {
		h.keys("down")
	}
	h.keys("enter")
}

func TestFolderPickerNewFolder(t *testing.T) {
	launch := t.TempDir()
	h := newHarness(t, listSnap(), "a")
	h.m.SetLaunchDir(launch)

	// A plain name is created under the launch folder.
	openNewFolder(h)
	h.wantScreen("New folder:")
	h.typeText("fresh")
	h.keys("enter")
	want := filepath.Join(launch, "fresh")
	if got := h.f.news[len(h.f.news)-1]; got != "default "+want {
		t.Errorf("new folder: %s", got)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Errorf("folder not created: %v", err)
	}

	// An absolute path is created where it says.
	elsewhere := filepath.Join(t.TempDir(), "a", "b")
	openNewFolder(h)
	h.m.input.SetValue(elsewhere)
	h.keys("enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default "+elsewhere {
		t.Errorf("new folder elsewhere: %s", got)
	}
	if st, err := os.Stat(elsewhere); err != nil || !st.IsDir() {
		t.Errorf("folder not created: %v", err)
	}

	// No name starts nothing.
	n := len(h.f.news)
	openNewFolder(h)
	h.keys("enter")
	if len(h.f.news) != n {
		t.Error("started a session with no folder name")
	}
}
