package sidebar

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/repo"
)

// ---- folder picker (SPEC §6.1 step 2) ----

type folderKind int

const (
	fkLaunch   folderKind = iota // the folder acmd was run from
	fkSelected                   // the shown/selected session's repository
	fkRecent                     // a recently used repository
	fkPath                       // an existing folder typed in the box
	fkDir                        // a subfolder matching what's typed (autocomplete)
	fkCreate                     // a folder typed in the box that doesn't exist yet
	fkNew                        // "New folder…"
)

// maxDirSuggestions caps the autocomplete rows so the folders already
// known stay near the top.
const maxDirSuggestions = 6

type folderItem struct {
	kind folderKind
	path string
}

// folderPicker is where a new session in a group starts. groupID is the
// group, or "" with createGroup set when the group is created on start.
type folderPicker struct {
	groupID     string
	createGroup string
	sel, top    int // highlighted item; first item shown (the list scrolls)

	// Looked up once per path while the picker is open: folderItems runs
	// on every key and redraw, and these hit the disk (or spawn git).
	roots     map[string]string     // cwd → repository root
	stats     map[string]pathState  // typed or linked path → what's there
	subdirs   map[string][]dirEntry // folder → its folders and symlinks
	worktrees map[string]bool       // folder → is a linked worktree's top
}

// pathState is what a path names: a folder, nothing (so it can be
// created), or neither (a file, or it couldn't be checked).
type pathState struct{ isDir, missing bool }

func (f *folderPicker) stat(p string) pathState {
	if s, ok := f.stats[p]; ok {
		return s
	}
	st, err := os.Stat(p)
	s := pathState{isDir: err == nil && st.IsDir(), missing: errors.Is(err, fs.ErrNotExist)}
	if f.stats == nil {
		f.stats = map[string]pathState{}
	}
	f.stats[p] = s
	return s
}

// dirEntry is a folder, or a symlink that may lead to one (only resolved
// when it would be offered).
type dirEntry struct {
	name, lower string
	link        bool
}

// dirs lists dir's folders and symlinks, sorted ignoring case. An
// unreadable or missing folder has none.
func (f *folderPicker) dirs(dir string) []dirEntry {
	if d, ok := f.subdirs[dir]; ok {
		return d
	}
	var out []dirEntry
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if link := e.Type()&fs.ModeSymlink != 0; e.IsDir() || link {
			out = append(out, dirEntry{name: e.Name(), lower: strings.ToLower(e.Name()), link: link})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lower < out[j].lower })
	if f.subdirs == nil {
		f.subdirs = map[string][]dirEntry{}
	}
	f.subdirs[dir] = out
	return out
}

func (f *folderPicker) isWorktree(dir string) bool {
	if w, ok := f.worktrees[dir]; ok {
		return w
	}
	if f.worktrees == nil {
		f.worktrees = map[string]bool{}
	}
	w := repo.IsWorktreeTop(dir)
	f.worktrees[dir] = w
	return w
}

// completions are the subfolders that complete text: those of the folder
// before its last "/" (the launch folder for a plain name, home for "~")
// whose names start with what follows it, ignoring case. Hidden folders
// are only offered once a "." is typed, and worktrees never are (a
// worktree should only appear when Claude makes one; SPEC §6.1).
func (m *Model) completions(text string) []string {
	// Split the text as typed: cleaning the path would turn "x/." into "x".
	parent, prefix := m.folderBase(), text
	switch i := strings.LastIndex(text, "/"); {
	case text == "~":
		parent, prefix = m.home, ""
	case i >= 0:
		parent, prefix = m.expandPath(text[:i+1]), text[i+1:]
	}
	prefix = strings.ToLower(prefix)
	var out []string
	for _, e := range m.fpick.dirs(parent) {
		if !strings.HasPrefix(e.lower, prefix) || (strings.HasPrefix(e.name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		p := filepath.Join(parent, e.name)
		if (e.link && !m.fpick.stat(p).isDir) || m.fpick.isWorktree(p) {
			continue
		}
		out = append(out, p)
		if len(out) == maxDirSuggestions {
			break
		}
	}
	return out
}

// root is repo.Root(dir), remembered while the picker is open: folderItems
// runs on every key and every redraw, and each uncached lookup spawns git.
func (f *folderPicker) root(dir string) string {
	if r, ok := f.roots[dir]; ok {
		return r
	}
	if f.roots == nil {
		f.roots = map[string]string{}
	}
	r := repo.Root(dir)
	f.roots[dir] = r
	return r
}

// SetLaunchDir sets the folder acmd was run from, offered first when a
// session is started. Worktrees are never offered: a new session starts in
// the repository and Claude makes its own worktree.
func (m *Model) SetLaunchDir(dir string) { m.launchDir = repo.Root(dir) }

// openFolderPicker asks where the new session starts.
func (m *Model) openFolderPicker(groupID, createGroup string) tea.Cmd {
	m.fpick, m.mode = &folderPicker{groupID: groupID, createGroup: createGroup}, modeFolder
	m.input.SetValue("")
	m.input.SetWidth(max(4, m.w-4))
	return m.input.Focus()
}

// folderBase is the folder relative paths and new folder names are
// resolved against.
func (m *Model) folderBase() string {
	if m.launchDir != "" {
		return m.launchDir
	}
	return m.home
}

// expandPath resolves a typed path: ~ is home, a relative path is under
// the launch folder.
func (m *Model) expandPath(p string) string {
	p = strings.TrimSpace(p)
	switch {
	case p == "~":
		return m.home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(m.home, p[2:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(m.folderBase(), p)
}

func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")
}

// folderItems lists the choices: the launch folder, the shown session's
// repository, recent repositories (worktrees collapsed into theirs), and
// "New folder…". Typed text filters them and autocompletes: the typed
// folder when it exists, subfolders that complete it, and, when it
// doesn't exist yet, a choice that creates it (replacing "New folder…").
// A path (~, /, ./, ../) filters by prefix and its completions come
// first; a plain name filters by substring and is resolved under the
// launch folder, after the known folders it matches.
func (m *Model) folderItems() []folderItem {
	text := strings.TrimSpace(m.input.Value())
	pathLike := looksLikePath(text)
	target := ""
	if text != "" {
		target = m.expandPath(text)
	}
	// The folders acmd knows of, each with its marker.
	var known []folderItem
	knownKind := map[string]folderKind{}
	addKnown := func(k folderKind, dir string) {
		if _, dup := knownKind[dir]; dir != "" && !dup {
			knownKind[dir] = k
			known = append(known, folderItem{kind: k, path: dir})
		}
	}
	addKnown(fkLaunch, m.launchDir)
	if m.view != nil {
		if s := m.session(m.view.Selected); s != nil && s.Cwd != "" {
			addKnown(fkSelected, m.fpick.root(s.Cwd))
		}
	}
	for _, d := range m.recentRoots() {
		addKnown(fkRecent, d)
	}
	if len(known) == 0 {
		addKnown(fkLaunch, m.home)
	}
	if text == "" {
		return append(known, folderItem{kind: fkNew})
	}

	// A path matches the known folders under it ("x/" only those inside x,
	// "x" also x-old); a plain name, those containing it.
	match := func(dir string) bool {
		return strings.Contains(strings.ToLower(dir), strings.ToLower(text))
	}
	if pathLike {
		under := strings.ToLower(target)
		if strings.HasSuffix(text, "/") {
			under = strings.TrimSuffix(under, "/") + "/"
		}
		match = func(dir string) bool { return strings.HasPrefix(strings.ToLower(dir)+"/", under) }
	}
	seen := map[string]bool{}
	var out []folderItem
	add := func(k folderKind, dir string) {
		if !seen[dir] {
			seen[dir] = true
			out = append(out, folderItem{kind: k, path: dir})
		}
	}
	st := m.fpick.stat(target)
	typed := func() {
		if st.isDir {
			k, ok := knownKind[target] // keeps its ⌂/● marker
			if !ok {
				k = fkPath
			}
			add(k, target)
		}
		for _, d := range m.completions(text) {
			add(fkDir, d)
		}
	}
	if pathLike {
		typed()
	}
	for _, it := range known {
		if match(it.path) {
			add(it.kind, it.path)
		}
	}
	if !pathLike {
		typed()
	}
	// Only a path that's certainly not there can be created: not a file,
	// nor a folder that couldn't be checked.
	if st.missing && !seen[target] {
		return append(out, folderItem{kind: fkCreate, path: target})
	}
	return append(out, folderItem{kind: fkNew})
}

// recentRoots are the repositories of the most recently active sessions,
// up to 8, most recent first.
func (m *Model) recentRoots() []string {
	type recent struct {
		dir string
		at  int64
	}
	last := map[string]int64{}
	for _, s := range m.snap.Sessions {
		if s.Cwd == "" {
			continue
		}
		root := m.fpick.root(s.Cwd)
		if at := s.LastEventAt.UnixNano(); at >= last[root] {
			last[root] = at
		}
	}
	var rs []recent
	for d, at := range last {
		rs = append(rs, recent{d, at})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].at != rs[j].at {
			return rs[i].at > rs[j].at
		}
		return rs[i].dir < rs[j].dir
	})
	var out []string
	for i, r := range rs {
		if i >= 8 {
			break
		}
		out = append(out, r.dir)
	}
	return out
}

func (m *Model) folderKey(k tea.KeyPressMsg) tea.Cmd {
	items := m.folderItems()
	switch k.String() {
	case "up":
		m.fpick.sel = max(0, m.fpick.sel-1)
		return nil
	case "down":
		m.fpick.sel = min(len(items)-1, m.fpick.sel+1)
		return nil
	case "esc":
		m.mode, m.fpick = modeNormal, nil
		m.input.Blur()
		m.note = "Canceled."
		return nil
	case "enter":
		return m.chooseFolder(items[min(m.fpick.sel, len(items)-1)])
	case "tab":
		m.complete(items[min(m.fpick.sel, len(items)-1)])
		return nil
	}
	return m.folderInput(k)
}

// folderInput passes a key or a paste to the box. New text gives a new
// list, so the highlight goes back to its first choice.
func (m *Model) folderInput(msg tea.Msg) tea.Cmd {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.fpick.sel, m.fpick.top = 0, 0
	}
	return cmd
}

// folderRows is how many choices fit between the box and the hint line.
func (m *Model) folderRows() int { return max(1, m.bodyHeight()-3) }

func (m *Model) folderClick(mo tea.Mouse) tea.Cmd {
	i := mo.Y - bodyTop - 2 // title and input lines
	if i < 0 || i >= m.folderRows() {
		return nil // the hint, or below it
	}
	i += m.fpick.top
	items := m.folderItems()
	if i >= len(items) {
		return nil
	}
	m.fpick.sel = i
	if mo.Button == tea.MouseLeft {
		return m.chooseFolder(items[i])
	}
	return nil
}

// complete fills the box with the highlighted folder and a "/", so its
// subfolders are offered next. "New folder…" and a folder still to be
// created have nothing to complete.
func (m *Model) complete(it folderItem) {
	if it.kind == fkNew || it.kind == fkCreate || it.path == "" {
		return
	}
	v := tilde(it.path, m.home)
	if !strings.HasSuffix(v, "/") {
		v += "/"
	}
	m.input.SetValue(v)
	m.input.CursorEnd()
	m.fpick.sel, m.fpick.top = 0, 0
}

// chooseFolder starts the session in the folder, or asks for the name of
// a new one.
func (m *Model) chooseFolder(it folderItem) tea.Cmd {
	fp := m.fpick
	m.mode, m.fpick = modeNormal, nil
	m.input.Blur()
	switch it.kind {
	case fkCreate: // typed, maybe mistyped: ask before making a folder
		return m.ask2("Create "+tilde(it.path, m.home)+" and start a session there?", func() tea.Cmd {
			return m.startIn(fp, it.path, true)
		})
	case fkNew:
	default:
		return m.startIn(fp, it.path, false)
	}
	base := tilde(m.folderBase(), m.home)
	return m.ask("New folder", base+"/", func(v string) tea.Cmd {
		v = strings.TrimSpace(v)
		if v == "" || m.expandPath(v) == m.folderBase() {
			m.note, m.noteErr = "Type a name for the new folder.", true
			return nil
		}
		return m.startIn(fp, m.expandPath(v), true)
	})
}

// startIn starts the session in dir (creating dir first when asked, and
// the group when it's new).
func (m *Model) startIn(fp *folderPicker, dir string, create bool) tea.Cmd {
	m.note = "Starting a session in " + tilde(dir, m.home) + "…"
	return m.do(func(ctx context.Context) error {
		if create {
			if st, err := os.Stat(dir); err == nil && !st.IsDir() {
				return fmt.Errorf("%s is a file, not a folder", tilde(dir, m.home))
			}
			if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- an ordinary project folder
				return fmt.Errorf("can't create %s: %w", tilde(dir, m.home), err)
			}
		}
		groupID := fp.groupID
		if fp.createGroup != "" {
			var g core.Group
			if err := m.act.Mutate(ctx, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: fp.createGroup}, &g); err != nil {
				return err
			}
			groupID = g.ID
		}
		return m.act.New(ctx, groupID, dir)
	})
}

// folderLines draws the picker, scrolled so the highlighted choice and the
// hint line stay on screen.
func (m *Model) folderLines() []string {
	out := []string{boldStyle.Render(fit(" Start in:", m.w)), " > " + m.input.View()}
	items, rows, fp := m.folderItems(), m.folderRows(), m.fpick
	fp.sel = min(fp.sel, len(items)-1)
	fp.top = max(0, min(fp.top, fp.sel, len(items)-rows))
	if fp.sel >= fp.top+rows {
		fp.top = fp.sel - rows + 1
	}
	for i := fp.top; i < len(items) && i < fp.top+rows; i++ {
		it := items[i]
		var label string
		switch it.kind {
		case fkLaunch:
			label = "⌂ " + tilde(it.path, m.home)
		case fkSelected:
			label = "● " + tilde(it.path, m.home)
		case fkRecent:
			label = "  " + tilde(it.path, m.home)
		case fkPath:
			label = "… " + tilde(it.path, m.home)
		case fkDir:
			label = "▸ " + tilde(it.path, m.home) + "/"
		case fkCreate:
			label = "＋ New folder " + tilde(it.path, m.home)
		default:
			label = "＋ New folder…"
		}
		label = fit("   "+label, m.w)
		if i == fp.sel {
			label = cursorStyle.Render(padTo(ansi.Strip(label), m.w))
		}
		out = append(out, label)
	}
	return append(out, dimStyle.Render(fit(" ⌂ here · ● shown · ⇥ complete · ↵ choose · esc cancel", m.w)))
}
