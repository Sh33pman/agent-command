package sidebar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// mkdirs makes each folder under root.
func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFolderPickerTypedPath(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, "code/x")
	h := newHarness(t, listSnap(), "a")
	h.m.home = home
	h.m.SetLaunchDir("/w/launch")
	h.keys("n")
	h.typeText("~/code/x")
	h.wantScreen("… ~/code/x")
	h.keys("enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default "+filepath.Join(home, "code/x") {
		t.Errorf("typed path: %s", got)
	}
	if strings.Contains(h.screen(), "New folder ~/code/x") {
		t.Error("an existing folder is offered for creation")
	}
}

func TestFolderPickerAutocompletesSubfolders(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, "code/alpha", "code/Apple", "code/beta", "code/.hidden", "code/alpha/inner")
	if err := os.WriteFile(filepath.Join(home, "code", "afile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, listSnap(), "a")
	h.m.home = home
	h.m.SetLaunchDir("/w/launch")
	h.keys("n")

	// A partial name lists the matching subfolders (ignoring case), not
	// files, other folders or hidden ones.
	h.typeText("~/code/a")
	h.wantScreen("▸ ~/code/alpha/", "▸ ~/code/Apple/", "＋ New folder ~/code/a")
	for _, no := range []string{"beta", "afile", ".hidden", "⌂ /w/launch"} {
		if strings.Contains(h.screen(), no) {
			t.Errorf("%q offered:\n%s", no, h.screen())
		}
	}

	// Tab completes the highlighted folder; its subfolders come next.
	h.keys("tab")
	if got := h.m.input.Value(); got != "~/code/alpha/" {
		t.Errorf("completed to %q", got)
	}
	h.wantScreen("… ~/code/alpha", "▸ ~/code/alpha/inner/")

	// Down to the subfolder and Enter starts there.
	h.keys("down", "enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default "+filepath.Join(home, "code/alpha/inner") {
		t.Errorf("chosen: %s", got)
	}

	// A "." shows hidden folders.
	h.keys("n")
	h.typeText("~/code/.")
	h.wantScreen("▸ ~/code/.hidden/")
}

func TestFolderPickerPlainNameFiltersAndCompletes(t *testing.T) {
	launch := t.TempDir()
	mkdirs(t, launch, "docs", "dotfiles")
	h := newHarness(t, listSnap(), "a")
	h.m.home = launch // keeps the rows short enough to read in the narrow sidebar
	h.m.SetLaunchDir(launch)
	h.f.snap.Sessions[0].Cwd = "/w/dots-repo"
	h.send(snapMsg{snap: h.f.snap, rev: 9})

	h.keys("n")
	h.typeText("do")
	// Known folders that match come first, then subfolders of the launch folder.
	h.wantScreen("/w/dots-repo", "▸ ~/docs/", "▸ ~/dotfiles/", "＋ New folder ~/do")
	scr := h.screen()
	if strings.Index(scr, "/w/dots-repo") > strings.Index(scr, "docs/") {
		t.Errorf("known folder not first:\n%s", scr)
	}
	h.keys("enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default /w/dots-repo" {
		t.Errorf("chosen: %s", got)
	}
}

func TestFolderPickerTypedNewFolderIsCreated(t *testing.T) {
	launch := t.TempDir()
	h := newHarness(t, listSnap(), "a")
	h.m.home = launch
	h.m.SetLaunchDir(launch)
	h.keys("n")
	h.typeText("brand-new")
	h.wantScreen("＋ New folder ~/brand-new")
	if strings.Contains(h.screen(), "New folder…") {
		t.Errorf("both create choices shown:\n%s", h.screen())
	}
	// It's the only choice, so Enter creates it; Tab has nothing to complete.
	h.keys("tab")
	if got := h.m.input.Value(); got != "brand-new" {
		t.Errorf("tab changed the box to %q", got)
	}
	// A typo shouldn't make a folder: Enter asks first, and only y creates.
	want := filepath.Join(launch, "brand-new")
	h.keys("enter")
	h.wantScreen("Create ~/brand-new and start a", "session there?")
	h.keys("n")
	if len(h.f.news) != 0 {
		t.Fatal("started without a yes")
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("folder made without a yes: %v", err)
	}
	h.keys("n")
	h.typeText("brand-new")
	h.keys("enter", "y")
	if got := h.f.news[len(h.f.news)-1]; got != "default "+want {
		t.Errorf("new folder: %s", got)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Errorf("folder not created: %v", err)
	}
}

func TestFolderPickerSkipsWorktreesAndKeepsMarkers(t *testing.T) {
	launch := t.TempDir()
	mkdirs(t, launch, "repo", "repo-wt")
	if err := os.WriteFile(filepath.Join(launch, "repo-wt", ".git"), []byte("gitdir: /x/repo/.git/worktrees/repo-wt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, listSnap(), "a")
	h.m.home = launch
	h.m.SetLaunchDir(launch)
	h.keys("n")
	h.typeText("~/re")
	h.wantScreen("▸ ~/repo/")
	if strings.Contains(h.screen(), "repo-wt") {
		t.Errorf("a linked worktree is offered:\n%s", h.screen())
	}

	// Typing the launch folder keeps its ⌂ rather than showing it twice.
	h.keys("esc", "n")
	h.typeText("~")
	h.wantScreen("⌂ ~")
	if strings.Contains(h.screen(), "… ~") {
		t.Errorf("launch folder shown as a typed path:\n%s", h.screen())
	}
}

func TestFolderPickerScrollsAndPasteResets(t *testing.T) {
	home := t.TempDir()
	var many []string
	for _, c := range "abcdefgh" {
		many = append(many, "d/"+string(c))
	}
	mkdirs(t, home, many...)
	h := newHarness(t, listSnap(), "a")
	h.m.home = home
	h.m.SetLaunchDir("/w/launch")
	h.send(tea.WindowSizeMsg{Width: 34, Height: 14}) // too short for all 9 choices
	h.keys("n")
	h.typeText("~/d/")
	rows := h.m.folderRows()
	if rows >= 9 {
		t.Fatalf("%d rows fit; the test needs fewer", rows)
	}
	for range rows + 1 { // past the last row that fits ("… ~/d" is the first)
		h.keys("down")
	}
	// The highlighted choice is on screen, and so is the hint.
	if sel := h.m.folderItems()[h.m.fpick.sel]; !strings.Contains(h.screen(), tilde(sel.path, home)+"/") {
		t.Errorf("highlighted %s not shown:\n%s", sel.path, h.screen())
	}
	h.wantScreen("⇥ complete")
	if strings.Contains(h.screen(), "… ~/d") {
		t.Errorf("not scrolled:\n%s", h.screen())
	}

	// A paste gives a new list: the highlight goes back to its top.
	h.send(tea.PasteMsg{Content: "x"})
	if h.m.fpick.sel != 0 || h.m.fpick.top != 0 {
		t.Errorf("after paste sel=%d top=%d", h.m.fpick.sel, h.m.fpick.top)
	}
}

func TestFolderPickerTypedFileIsNotAChoice(t *testing.T) {
	launch := t.TempDir()
	if err := os.WriteFile(filepath.Join(launch, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, listSnap(), "a")
	h.m.SetLaunchDir(launch)
	h.keys("n")
	h.typeText("./notes.txt")
	for _, it := range h.m.folderItems() {
		if it.kind != fkNew {
			t.Errorf("a file is offered: %+v", it)
		}
	}
	h.wantScreen("New folder…")
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
