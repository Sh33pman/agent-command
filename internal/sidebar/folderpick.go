package sidebar

import (
	"context"
	"fmt"
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
	fkPath                       // a path typed in the box
	fkNew                        // "New folder…"
)

type folderItem struct {
	kind folderKind
	path string
}

// folderPicker is where a new session in a group starts. groupID is the
// group, or "" with createGroup set when the group is created on start.
type folderPicker struct {
	groupID     string
	createGroup string
	sel         int
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
// repository, recent repositories (worktrees collapsed into theirs), the
// typed path, and "New folder…". Typed text filters the folders.
func (m *Model) folderItems() []folderItem {
	text := strings.TrimSpace(m.input.Value())
	seen := map[string]bool{}
	var out []folderItem
	add := func(k folderKind, dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		if text != "" && !looksLikePath(text) && !strings.Contains(strings.ToLower(dir), strings.ToLower(text)) {
			return
		}
		out = append(out, folderItem{kind: k, path: dir})
	}
	if looksLikePath(text) {
		p := m.expandPath(text)
		seen[p] = true
		out = append(out, folderItem{kind: fkPath, path: p})
	}
	add(fkLaunch, m.launchDir)
	if m.view != nil {
		if s := m.session(m.view.Selected); s != nil && s.Cwd != "" {
			add(fkSelected, repo.Root(s.Cwd))
		}
	}
	type recent struct {
		dir string
		at  int64
	}
	last := map[string]int64{}
	for _, s := range m.snap.Sessions {
		if s.Cwd == "" {
			continue
		}
		root := repo.Root(s.Cwd)
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
	for i, r := range rs {
		if i >= 8 {
			break
		}
		add(fkRecent, r.dir)
	}
	if len(out) == 0 && text == "" {
		add(fkLaunch, m.home)
	}
	return append(out, folderItem{kind: fkNew})
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
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.input.Value() != before {
		m.fpick.sel = 0
	}
	return cmd
}

func (m *Model) folderClick(mo tea.Mouse) tea.Cmd {
	i := mo.Y - bodyTop - 2 // title and input lines
	items := m.folderItems()
	if i < 0 || i >= len(items) {
		return nil
	}
	m.fpick.sel = i
	if mo.Button == tea.MouseLeft {
		return m.chooseFolder(items[i])
	}
	return nil
}

// chooseFolder starts the session in the folder, or asks for the name of
// a new one.
func (m *Model) chooseFolder(it folderItem) tea.Cmd {
	fp := m.fpick
	m.mode, m.fpick = modeNormal, nil
	m.input.Blur()
	if it.kind != fkNew {
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

func (m *Model) folderLines() []string {
	out := []string{boldStyle.Render(fit(" Start in:", m.w)), " > " + m.input.View()}
	for i, it := range m.folderItems() {
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
		default:
			label = "＋ New folder…"
		}
		label = fit("   "+label, m.w)
		if i == m.fpick.sel {
			label = cursorStyle.Render(padTo(ansi.Strip(label), m.w))
		}
		out = append(out, label)
	}
	return append(out, dimStyle.Render(fit(" ⌂ here · ● shown · ↵ choose · esc cancel", m.w)))
}
