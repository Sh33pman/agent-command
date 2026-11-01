package sidebar

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// ---- group picker (SPEC §8.4) ----

type pickKind int

const (
	pickNew pickKind = iota
	pickMove
)

type picker struct {
	kind  pickKind
	dir   string   // pickNew: folder ("" = the group's)
	ids   []string // pickMove: sessions to move
	sel   int
	label string
}

// pickItem is a group, or (group nil) "create group <text>".
type pickItem struct {
	group  *core.Group
	create string
}

func (m *Model) openPicker(kind pickKind, preselect, dir string) tea.Cmd {
	p := &picker{kind: kind, dir: dir, label: "New session in group:"}
	if kind == pickMove {
		for _, s := range m.targets() {
			p.ids = append(p.ids, s.ID)
		}
		p.label = "Move " + plural(len(p.ids), "session") + " to:"
		if len(p.ids) == 1 {
			p.label = "Move " + quote(name(m.session(p.ids[0]))) + " to:"
		}
	}
	m.picker, m.mode = p, modePicker
	m.input.SetValue("")
	m.input.SetWidth(max(4, m.w-4))
	for i, it := range m.pickItems() {
		if it.group != nil && it.group.ID == preselect {
			p.sel = i
		}
	}
	return m.input.Focus()
}

// pickItems lists live groups in sidebar order (Default last) matching
// the typed text, plus "create" when no group has exactly that name.
func (m *Model) pickItems() []pickItem {
	text := strings.TrimSpace(m.input.Value())
	var groups []*core.Group
	var def *core.Group
	for _, g := range m.snap.Groups {
		if !g.ArchivedAt.IsZero() {
			continue
		}
		if g.IsDefault {
			def = g
			continue
		}
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].SortKey < groups[j].SortKey })
	if def != nil {
		groups = append(groups, def)
	}
	var out []pickItem
	exact := false
	for _, g := range groups {
		if strings.EqualFold(g.Name, text) {
			exact = true
		}
		if text == "" || strings.Contains(strings.ToLower(g.Name), strings.ToLower(text)) {
			out = append(out, pickItem{group: g})
		}
	}
	if text != "" && !exact {
		out = append(out, pickItem{create: text})
	}
	return out
}

func (m *Model) pickerKey(k tea.KeyPressMsg) tea.Cmd {
	items := m.pickItems()
	switch k.String() {
	case "up":
		m.picker.sel = max(0, m.picker.sel-1)
		return nil
	case "down":
		m.picker.sel = min(len(items)-1, m.picker.sel+1)
		return nil
	case "esc":
		m.mode, m.picker = modeNormal, nil
		m.input.Blur()
		m.note = "Canceled."
		return nil
	case "enter":
		if len(items) == 0 {
			return nil
		}
		return m.pick(items[min(m.picker.sel, len(items)-1)])
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.input.Value() != before {
		m.picker.sel = 0
	}
	return cmd
}

func (m *Model) pickerClick(mo tea.Mouse) tea.Cmd {
	i := mo.Y - bodyTop - 2 // title and input lines
	items := m.pickItems()
	if i < 0 || i >= len(items) {
		return nil
	}
	m.picker.sel = i
	if mo.Button == tea.MouseLeft {
		return m.pick(items[i])
	}
	return nil
}

// pick finishes the picker with it (creating the group first if needed).
func (m *Model) pick(it pickItem) tea.Cmd {
	p := m.picker
	m.mode, m.picker = modeNormal, nil
	m.input.Blur()
	if p.kind == pickMove {
		m.marked = map[string]bool{}
	}
	return m.do(func(ctx context.Context) error {
		groupID := ""
		if it.group != nil {
			groupID = it.group.ID
		} else {
			var g core.Group
			if err := m.act.Mutate(ctx, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: it.create}, &g); err != nil {
				return err
			}
			groupID = g.ID
		}
		if p.kind == pickMove {
			return m.act.Mutate(ctx, ipc.Mutation{Kind: ipc.MutMove, IDs: p.ids, GroupID: groupID}, nil)
		}
		dir := p.dir
		if dir == "" {
			dir = m.folderFor(groupID)
		}
		return m.act.New(ctx, groupID, dir)
	})
}

// ---- context menu (SPEC §8.2 right-click) ----

type menuItem struct {
	label string
	run   func() tea.Cmd
}

type menu struct {
	title string
	items []menuItem
	sel   int
	y     int // first item's screen line
}

func (m *Model) openMenu(r core.Row, y int) {
	var items []menuItem
	title := ""
	if s := r.Session; s != nil {
		title = name(s)
		items = []menuItem{
			{"Show", func() tea.Cmd { return m.show(s) }},
			{"Rename", func() tea.Cmd { return m.rename(r) }},
			{"Edit description", func() tea.Cmd { return m.describe(s) }},
			{"Open in editor", func() tea.Cmd { return m.do(func(ctx context.Context) error { return m.act.Open(ctx, s) }) }},
			{"Move to group…", func() tea.Cmd { return m.openPicker(pickMove, s.GroupID, "") }},
			{"Finish", func() tea.Cmd { return m.finish(s) }},
			{"Archive", func() tea.Cmd { return m.archive([]*core.Session{s}) }},
		}
		if s.Kind == core.KindExternal {
			items = append(items, menuItem{"Move into acmd", func() tea.Cmd { return m.adopt(s) }})
		} else {
			items = append(items, menuItem{"Split out…", func() tea.Cmd { m.openSplitMenu(s, y); return nil }})
		}
	} else {
		g := r.Group
		title = g.Name
		items = []menuItem{{"New session here", func() tea.Cmd { return m.newIn(g.ID, "") }}}
		if !g.IsDefault {
			items = append(items,
				menuItem{"Rename", func() tea.Cmd { return m.rename(r) }},
				menuItem{"Color: " + nextColor(g.Color), func() tea.Cmd {
					return m.mutate(ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: g.ID, Color: nextColor(g.Color)})
				}})
		}
		items = append(items,
			menuItem{"Focus", func() tea.Cmd { return m.focusGroup(r, true) }},
			menuItem{"Collapse others", func() tea.Cmd { return m.collapseOthers(g.ID) }})
		if !g.IsDefault {
			items = append(items,
				menuItem{"Archive group", func() tea.Cmd { return m.archiveGroup(g, r.Count) }},
				menuItem{"Delete group", func() tea.Cmd { return m.deleteGroup(g) }})
		}
	}
	// Below the row if it fits, else above.
	top := y + 1
	if top+len(items)+1 > m.h-footerMin {
		top = max(bodyTop, y-len(items)-1)
	}
	m.menu, m.mode = &menu{title: title, items: items, y: top + 1}, modeMenu
}

func (m *Model) collapseOthers(keep string) tea.Cmd {
	var cmds []tea.Cmd
	for _, g := range m.snap.Groups {
		if g.ArchivedAt.IsZero() && g.Collapsed == (g.ID == keep) {
			cmds = append(cmds, m.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g.ID, Flag: g.ID != keep}))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) menuKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.menu.sel = max(0, m.menu.sel-1)
	case "down", "j":
		m.menu.sel = min(len(m.menu.items)-1, m.menu.sel+1)
	case "enter":
		run := m.menu.items[m.menu.sel].run
		m.mode, m.menu = modeNormal, nil
		return run()
	case "esc", "q":
		m.mode, m.menu = modeNormal, nil
	}
	return nil
}

func (m *Model) menuClick(mo tea.Mouse) tea.Cmd {
	i := mo.Y - m.menu.y
	if mo.Button != tea.MouseLeft || i < 0 || i >= len(m.menu.items) {
		m.mode, m.menu = modeNormal, nil
		return nil
	}
	run := m.menu.items[i].run
	m.mode, m.menu = modeNormal, nil
	return run()
}

func nextColor(c string) string {
	for i, p := range store.Palette {
		if p == c {
			return store.Palette[(i+1)%len(store.Palette)]
		}
	}
	return store.Palette[0]
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
