// Package sidebar is a view's session list (SPEC §8.1, §8.2, §8.4), a
// Bubble Tea program: the shared list (groups or list view, sort, filter,
// collapse, focus) with this view's selection, keyboard and mouse control,
// inline rename/describe, confirmations, the group picker and a context
// menu. All state changes go through the daemon; the sidebar re-renders
// from the snapshots it watches.
package sidebar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/tui"
)

// Actions are the sidebar's side effects (daemon and terminal).
type Actions interface {
	// Watch waits for a state change after rev (passive: it isn't the
	// view's heartbeat).
	Watch(ctx context.Context, rev uint64, wait bool) (ipc.WatchResponse, error)
	Snapshot(ctx context.Context) (ipc.Snapshot, error)
	Mutate(ctx context.Context, m ipc.Mutation, out any) error
	// Show shows s in this view and focuses the view pane (external
	// sessions: focus their own pane).
	Show(ctx context.Context, s *core.Session) error
	New(ctx context.Context, groupID, dir string) error
	FocusView(ctx context.Context) error
	ToggleSidebar(ctx context.Context) error
	FitWidth(ctx context.Context) error
	// Open opens s's folder in the editor (SPEC §8.6).
	Open(ctx context.Context, s *core.Session) error
	// Help is the cheat sheet (SPEC §8.3.1), lines at most width wide.
	Help(width int, groups bool) []string
	// Adopt moves an external session into acmd (SPEC §6.3; confirmed).
	Adopt(ctx context.Context, s *core.Session) error
	// SplitOut opens s in a new tab or window (SPEC §6.6).
	SplitOut(ctx context.Context, s *core.Session, window, withSidebar bool) error
	// Terminal is "kitty" or "ghostty" (notification format).
	Terminal() string
	// ViewFocused reports whether this view's pane has focus in the
	// frontmost terminal (then no notification for what it shows).
	ViewFocused(ctx context.Context) bool
}

// ErrClosed means the sidebar's view was closed.
var ErrClosed = errors.New("view closed")

type mode int

const (
	modeNormal mode = iota
	modeInput       // rename / describe / folder prompt / filter
	modeConfirm
	modePicker
	modeMenu
	modeHelp
)

// Model is the sidebar.
type Model struct {
	ctx    context.Context
	act    Actions
	viewID string
	home   string
	now    func() time.Time

	snap   ipc.Snapshot
	view   *core.View
	rev    uint64
	loaded bool
	err    error // fatal: the program quits with it

	cursor  string // "s:<session id>" or "g:<group id>"
	helpTop int    // first help line shown
	top     int    // first visible body line
	marked  map[string]bool
	compact bool
	w, h    int
	spin    int

	mode    mode
	input   textinput.Model
	inputOn func(value string) tea.Cmd // what Enter does in modeInput
	label   string                     // input / confirm prompt
	confirm func() tea.Cmd
	picker  *picker
	menu    *menu
	note    string
	noteErr bool
	// sentNote/sentErr: the message last sent to the status bar pane.
	sentNote string
	sentErr  bool

	lastNotice uint64    // newest notice seen
	notified   string    // session of the last notification this sidebar showed
	notifiedAt time.Time // when
}

// New returns the sidebar for view viewID.
func New(ctx context.Context, act Actions, viewID, home string) *Model {
	in := textinput.New()
	in.Prompt = ""
	return &Model{ctx: ctx, act: act, viewID: viewID, home: home, now: time.Now, marked: map[string]bool{}, input: in, w: 32, h: 24}
}

// Err is why the program ended (ErrClosed when the view closed).
func (m *Model) Err() error { return m.err }

// ---- messages ----

type snapMsg struct {
	snap ipc.Snapshot
	view *core.View
	rev  uint64
}
type goneMsg struct{}
type fatalMsg struct{ err error }
type noteMsg struct {
	text string
	err  bool
}
type tickMsg struct{}

func (m *Model) watch(rev uint64, wait bool) tea.Cmd {
	return func() tea.Msg {
		for {
			w, err := m.act.Watch(m.ctx, rev, wait)
			if err != nil {
				return fatalMsg{err}
			}
			if w.Gone {
				return goneMsg{}
			}
			if w.Rev == rev && wait {
				continue // heartbeat timeout, nothing changed
			}
			snap, err := m.act.Snapshot(m.ctx)
			if err != nil {
				return fatalMsg{err}
			}
			return snapMsg{snap: snap, view: w.View, rev: w.Rev}
		}
	}
}

// do runs an action and reports its error as a note.
func (m *Model) do(fn func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		if err := fn(m.ctx); err != nil {
			return noteMsg{text: err.Error(), err: true}
		}
		return nil
	}
}

func (m *Model) mutate(mu ipc.Mutation) tea.Cmd {
	return m.do(func(ctx context.Context) error { return m.act.Mutate(ctx, mu, nil) })
}

func tick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// Init starts watching the daemon and the spinner.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.watch(0, false), tick()) }

// ---- rows ----

func (m *Model) viewState() core.ViewState {
	vs := m.snap.ViewState
	if m.mode == modeInput && m.label == "Filter" {
		vs.Filter = m.input.Value() // live while typing
	}
	return vs
}

func (m *Model) rows() []core.Row {
	return core.Layout(m.snap.Sessions, m.snap.Groups, m.viewState())
}

func key(r core.Row) string {
	if r.Session != nil {
		return "s:" + r.Session.ID
	}
	return "g:" + r.Group.ID
}

// current returns the row under the cursor (ok false when there are none).
func (m *Model) current() (core.Row, bool) {
	rows := m.rows()
	for _, r := range rows {
		if key(r) == m.cursor {
			return r, true
		}
	}
	if len(rows) == 0 {
		return core.Row{}, false
	}
	return rows[0], true
}

func (m *Model) moveCursor(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	at := 0
	for i, r := range rows {
		if key(r) == m.cursor {
			at = i
		}
	}
	at = max(0, min(len(rows)-1, at+delta))
	m.cursor = key(rows[at])
}

func (m *Model) groupsExist() bool {
	for _, g := range m.snap.Groups {
		if !g.IsDefault && g.ArchivedAt.IsZero() {
			return true
		}
	}
	return false
}

func (m *Model) session(id string) *core.Session {
	for _, s := range m.snap.Sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (m *Model) group(id string) *core.Group {
	for _, g := range m.snap.Groups {
		if g.ID == id {
			return g
		}
	}
	return nil
}

// targets are the marked sessions, else the one under the cursor.
func (m *Model) targets() []*core.Session {
	var out []*core.Session
	for _, s := range m.snap.Sessions {
		if m.marked[s.ID] {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		return out
	}
	if r, ok := m.current(); ok && r.Session != nil {
		return []*core.Session{r.Session}
	}
	return nil
}

// folderFor is where a new session in group starts (SPEC §6.1 step 2).
func (m *Model) folderFor(groupID string) string {
	if r, ok := m.current(); ok && r.Session != nil && r.Session.GroupID == groupID && r.Session.Cwd != "" {
		return r.Session.Cwd
	}
	if m.view != nil {
		if s := m.session(m.view.Selected); s != nil && s.GroupID == groupID && s.Cwd != "" {
			return s.Cwd
		}
	}
	var recent *core.Session
	for _, s := range m.snap.Sessions {
		if s.GroupID == groupID && s.Cwd != "" && (recent == nil || s.LastEventAt.After(recent.LastEventAt)) {
			recent = s
		}
	}
	if recent != nil {
		return recent.Cwd
	}
	return m.home
}

// currentGroup is the group to preselect: the cursor's, else the shown
// session's, else the most recently active session's (SPEC §8.4).
func (m *Model) currentGroup() string {
	if r, ok := m.current(); ok {
		return r.Group.ID
	}
	if m.view != nil {
		if s := m.session(m.view.Selected); s != nil {
			return s.GroupID
		}
	}
	return "default"
}

// ---- update ----

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	return m, tea.Batch(cmd, m.syncNote())
}

// hasBar reports whether the view has a full-width status bar pane; then
// messages go there instead of the sidebar's own bottom lines.
func (m *Model) hasBar() bool { return m.view != nil && m.view.Sidebar.BarWindow != 0 }

// syncNote sends the current message to the view's status bar pane when
// it changed.
func (m *Model) syncNote() tea.Cmd {
	isErr := m.noteErr && m.note != ""
	if !m.hasBar() || (m.note == m.sentNote && isErr == m.sentErr) {
		return nil
	}
	m.sentNote, m.sentErr = m.note, isErr
	return m.mutate(ipc.Mutation{Kind: ipc.MutViewNote, ID: m.viewID, Text: m.note, Flag: isErr})
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case snapMsg:
		first := !m.loaded
		m.snap, m.view, m.rev, m.loaded = msg.snap, msg.view, msg.rev, true
		notify := m.notices(msg.snap, first)
		if first && m.view != nil && m.view.Selected != "" {
			m.cursor = "s:" + m.view.Selected
		}
		for id := range m.marked {
			if m.session(id) == nil {
				delete(m.marked, id)
			}
		}
		return m, tea.Batch(m.watch(m.rev, true), notify)
	case emitMsg:
		if msg.sessionID != "" {
			m.notified, m.notifiedAt = msg.sessionID, m.now()
		}
		return m, tea.Raw(msg.seq)
	case tea.FocusMsg:
		return m, m.focused()
	case goneMsg:
		m.err = ErrClosed
		return m, tea.Quit
	case fatalMsg:
		m.err = msg.err
		return m, tea.Quit
	case noteMsg:
		m.note, m.noteErr = msg.text, msg.err
		return m, nil
	case tickMsg:
		m.spin++
		return m, tick()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, m.do(m.act.FitWidth) // proportional splits drift (M0 K3)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tea.MouseClickMsg:
		return m, m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		if msg.Mouse().Button == tea.MouseWheelUp {
			m.top = max(0, m.top-2)
		} else {
			m.top += 2
		}
		return m, nil
	}
	if m.mode == modeInput || m.mode == modePicker {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	m.note = ""
	switch m.mode {
	case modeInput:
		return m.inputKey(k)
	case modeConfirm:
		m.mode = modeNormal
		if s := k.String(); s == "y" || s == "Y" {
			return m.confirm()
		}
		m.note = "Canceled."
		return nil
	case modePicker:
		return m.pickerKey(k)
	case modeMenu:
		return m.menuKey(k)
	case modeHelp:
		switch k.String() {
		case "down", "j", "space":
			m.helpTop++
		case "up", "k":
			m.helpTop = max(0, m.helpTop-1)
		default:
			m.mode = modeNormal
		}
		return nil
	}
	if !m.loaded {
		return nil
	}
	s := k.String()
	r, ok := m.current()
	switch s {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "enter":
		if !ok {
			return nil
		}
		if r.Session == nil {
			return m.toggleCollapse(r.Group)
		}
		return m.show(r.Session)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n := int(s[0] - '0')
		for _, row := range m.rows() {
			if row.Session != nil && row.Number == n {
				m.cursor = key(row)
				return m.show(row.Session)
			}
		}
	case "n":
		if ok && r.Session == nil {
			return m.newIn(r.Group.ID, "")
		}
		return m.newSession("")
	case "N":
		dir := m.folderFor(m.currentGroup())
		return m.ask("Folder", dir, func(v string) tea.Cmd { return m.newSession(strings.TrimSpace(v)) })
	case "r":
		if !ok {
			return nil
		}
		return m.rename(r)
	case "e":
		if ok && r.Session != nil {
			return m.describe(r.Session)
		}
	case "m":
		if ts := m.targets(); len(ts) > 0 {
			return m.openPicker(pickMove, m.currentGroup(), "")
		}
	case "space":
		if !ok {
			return nil
		}
		if r.Session == nil {
			return m.toggleCollapse(r.Group)
		}
		m.marked[r.Session.ID] = !m.marked[r.Session.ID]
		if !m.marked[r.Session.ID] {
			delete(m.marked, r.Session.ID)
		}
		m.moveCursor(1)
	case "left":
		if ok && m.groupsExist() && m.snap.ViewState.Mode != "list" {
			m.cursor = "g:" + r.Group.ID
			if !r.Group.Collapsed {
				return m.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: r.Group.ID, Flag: true})
			}
		}
	case "right":
		if ok && r.Session == nil && r.Collapsed {
			return m.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: r.Group.ID, Flag: false})
		}
	case "f":
		return m.focusGroup(r, ok)
	case "z":
		anyOpen := false
		for _, row := range m.rows() {
			if row.Session == nil && !row.Collapsed {
				anyOpen = true
			}
		}
		return m.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, Flag: anyOpen})
	case "v":
		next := "list"
		if m.snap.ViewState.Mode == "list" {
			next = "groups"
		}
		return m.mutate(ipc.Mutation{Kind: ipc.MutViewMode, Text: next})
	case "o":
		return m.mutate(ipc.Mutation{Kind: ipc.MutViewSort, Text: nextSort(m.snap.ViewState.Sort), Flag: m.snap.ViewState.Desc})
	case "O":
		return m.mutate(ipc.Mutation{Kind: ipc.MutViewSort, Text: sortOrDefault(m.snap.ViewState.Sort), Flag: !m.snap.ViewState.Desc})
	case "K", "J":
		if ok && r.Session == nil && !r.Group.IsDefault {
			return m.mutate(ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: r.Group.ID, Flag: s == "J"})
		}
	case "D":
		if ok && r.Session == nil {
			return m.deleteGroup(r.Group)
		}
	case "a":
		if ok && r.Session == nil {
			return m.archiveGroup(r.Group, r.Count)
		}
		return m.archive(m.targets())
	case "A":
		return m.mutate(ipc.Mutation{Kind: ipc.MutViewArchived, Flag: !m.snap.ViewState.ShowArchived})
	case "/":
		return m.ask("Filter", m.snap.ViewState.Filter, func(v string) tea.Cmd {
			return m.mutate(ipc.Mutation{Kind: ipc.MutViewFilter, Text: strings.TrimSpace(v)})
		})
	case "tab":
		shown := ""
		if m.view != nil {
			shown = m.view.Selected
		}
		next := core.NextAttention(m.snap.Sessions, m.snap.Groups, m.snap.ViewState, shown)
		if next == nil {
			m.note = "No session needs you."
			return nil
		}
		var cmds []tea.Cmd
		if g := m.group(next.GroupID); g != nil && g.Collapsed {
			cmds = append(cmds, m.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g.ID, Flag: false}))
		}
		m.cursor = "s:" + next.ID
		return tea.Batch(append(cmds, m.show(next))...)
	case "h":
		return m.do(m.act.ToggleSidebar)
	case "q":
		return m.ask2("Close this view? Your sessions keep running.", func() tea.Cmd {
			return m.mutate(ipc.Mutation{Kind: ipc.MutViewClose, ID: m.viewID})
		})
	case "c":
		m.compact = !m.compact
	case "?":
		m.mode, m.helpTop = modeHelp, 0
	case "E":
		if ok && r.Session != nil {
			sess := r.Session
			m.note = "Opening " + tilde(sess.Cwd, m.home) + "…"
			return m.do(func(ctx context.Context) error { return m.act.Open(ctx, sess) })
		}
	case "M":
		if ok && r.Session != nil {
			return m.adopt(r.Session)
		}
	case "s":
		if ok && r.Session != nil {
			m.openSplitMenu(r.Session, m.cursorLine())
		}
	case "esc":
		switch {
		case m.snap.ViewState.Filter != "":
			return m.mutate(ipc.Mutation{Kind: ipc.MutViewFilter, Text: ""})
		case m.snap.ViewState.FocusedGroup != "":
			return m.mutate(ipc.Mutation{Kind: ipc.MutViewFocus})
		case len(m.marked) > 0:
			m.marked = map[string]bool{}
		default:
			return m.do(m.act.FocusView)
		}
	}
	m.scrollToCursor()
	return nil
}

func (m *Model) show(s *core.Session) tea.Cmd {
	if s.NeedsResume() {
		m.note = "Resuming " + quote(name(s)) + "…"
	}
	return m.do(func(ctx context.Context) error { return m.act.Show(ctx, s) })
}

func (m *Model) toggleCollapse(g *core.Group) tea.Cmd {
	return m.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g.ID, Flag: !g.Collapsed})
}

func (m *Model) focusGroup(r core.Row, ok bool) tea.Cmd {
	vs := m.snap.ViewState
	if vs.FocusedGroup != "" { // f again leaves focus mode (SPEC §8.2)
		return m.mutate(ipc.Mutation{Kind: ipc.MutViewFocus})
	}
	if !ok || !m.groupsExist() {
		return nil
	}
	list, id := vs.Mode == "list", r.Group.ID
	return m.do(func(ctx context.Context) error {
		if list { // focus applies to group view (SPEC §8.4)
			if err := m.act.Mutate(ctx, ipc.Mutation{Kind: ipc.MutViewMode, Text: "groups"}, nil); err != nil {
				return err
			}
		}
		return m.act.Mutate(ctx, ipc.Mutation{Kind: ipc.MutViewFocus, GroupID: id}, nil)
	})
}

var sorts = []string{"created", "name", "activity", "status", "cwd"}

func sortOrDefault(s string) string {
	if s == "" {
		return "created"
	}
	return s
}

func nextSort(cur string) string {
	for i, s := range sorts {
		if s == sortOrDefault(cur) {
			return sorts[(i+1)%len(sorts)]
		}
	}
	return sorts[0]
}

// ---- input ----

func (m *Model) ask(label, value string, on func(string) tea.Cmd) tea.Cmd {
	m.mode, m.label, m.inputOn = modeInput, label, on
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.SetWidth(max(4, m.w-len(label)-4))
	return m.input.Focus()
}

func (m *Model) inputKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.mode = modeNormal
		m.input.Blur()
		return m.inputOn(m.input.Value())
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.label == "Filter" {
		m.cursorToFirst()
	}
	return cmd
}

func (m *Model) cursorToFirst() {
	if rows := m.rows(); len(rows) > 0 {
		for _, r := range rows {
			if key(r) == m.cursor {
				return
			}
		}
		m.cursor = key(rows[0])
		m.top = 0
	}
}

func (m *Model) rename(r core.Row) tea.Cmd {
	if r.Session == nil {
		g := r.Group
		if g.IsDefault {
			m.note = "The Default group can't be renamed."
			return nil
		}
		return m.ask("Rename group", g.Name, func(v string) tea.Cmd {
			return m.mutate(ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: strings.TrimSpace(v)})
		})
	}
	s := r.Session
	return m.ask("Rename", s.Name, func(v string) tea.Cmd {
		return m.mutate(ipc.Mutation{Kind: ipc.MutRename, ID: s.ID, Text: v})
	})
}

func (m *Model) describe(s *core.Session) tea.Cmd {
	return m.ask("Describe", s.Description, func(v string) tea.Cmd {
		return m.mutate(ipc.Mutation{Kind: ipc.MutDescribe, ID: s.ID, Text: v})
	})
}

// ---- confirmations ----

func (m *Model) ask2(question string, then func() tea.Cmd) tea.Cmd {
	m.mode, m.label, m.confirm = modeConfirm, question, then
	return nil
}

func (m *Model) archive(ss []*core.Session) tea.Cmd {
	if len(ss) == 0 {
		return nil
	}
	what := fmt.Sprintf("%d sessions", len(ss))
	if len(ss) == 1 {
		what = quote(name(ss[0]))
	}
	running := 0
	for _, s := range ss {
		if s.Kind == core.KindBackground && s.Status != core.StatusPaused && s.Status != core.StatusArchived {
			running++
		}
	}
	q := "Archive " + what + "?"
	if running > 0 {
		q += " Running ones are stopped (Claude keeps the conversation)."
	}
	return m.ask2(q, func() tea.Cmd {
		var cmds []tea.Cmd
		for _, s := range ss {
			cmds = append(cmds, m.mutate(ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}))
		}
		m.marked = map[string]bool{}
		return tea.Batch(cmds...)
	})
}

func (m *Model) archiveGroup(g *core.Group, n int) tea.Cmd {
	if g.IsDefault {
		m.note = "The Default group can't be archived."
		return nil
	}
	return m.ask2(fmt.Sprintf("Archive group %s and its %d sessions?", quote(g.Name), n), func() tea.Cmd {
		return m.mutate(ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})
	})
}

func (m *Model) deleteGroup(g *core.Group) tea.Cmd {
	if g.IsDefault {
		m.note = "The Default group can't be deleted."
		return nil
	}
	return m.ask2(fmt.Sprintf("Delete group %s? Its sessions move to Default.", quote(g.Name)), func() tea.Cmd {
		return m.mutate(ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID})
	})
}

// ---- new session ----

// newSession starts a session (in dir, or the group's folder), asking for
// the group first when groups exist (SPEC §6.1 step 1).
func (m *Model) newSession(dir string) tea.Cmd {
	if m.groupsExist() {
		return m.openPicker(pickNew, m.currentGroup(), dir)
	}
	return m.newIn("default", dir)
}

func (m *Model) newIn(groupID, dir string) tea.Cmd {
	if dir == "" {
		dir = m.folderFor(groupID)
	}
	m.note = "Starting a session in " + tilde(dir, m.home) + "…"
	return m.do(func(ctx context.Context) error { return m.act.New(ctx, groupID, dir) })
}

// ---- mouse ----

func (m *Model) click(mo tea.Mouse) tea.Cmd {
	m.note = ""
	switch m.mode {
	case modeMenu:
		return m.menuClick(mo)
	case modePicker:
		return m.pickerClick(mo)
	case modeNormal:
	default:
		return nil
	}
	k, ok := m.keyAt(mo.Y)
	if !ok {
		return nil
	}
	m.cursor = k
	r, _ := m.current()
	switch mo.Button {
	case tea.MouseRight:
		m.openMenu(r, mo.Y)
		return nil
	case tea.MouseLeft:
		// No double-click rename: renaming is the r key (or the menu).
		if r.Session == nil {
			return m.toggleCollapse(r.Group)
		}
		return m.show(r.Session)
	}
	return nil
}

func quote(s string) string { return "“" + s + "”" }

func name(s *core.Session) string {
	if s.Name != "" {
		return s.Name
	}
	if s.BgID != "" {
		return "session " + s.BgID
	}
	return "session"
}

func tilde(p, home string) string { return tui.Tilde(p, home) }

// adopt asks, then moves an external session into acmd.
func (m *Model) adopt(s *core.Session) tea.Cmd {
	if s.Kind != core.KindExternal {
		m.note = "That session is already in acmd."
		return nil
	}
	return m.ask2("Move "+quote(name(s))+" into acmd? Claude continues it in the background; your pane goes back to the shell.", func() tea.Cmd {
		m.note = "Moving " + quote(name(s)) + " into acmd…"
		if cur := m.session(s.ID); cur != nil && cur.Status != core.StatusDone && cur.Status != core.StatusError && cur.Status != core.StatusPaused {
			m.note = "Will move " + quote(name(s)) + " into acmd once Claude is idle there…"
		}
		return m.do(func(ctx context.Context) error { return m.act.Adopt(ctx, s) })
	})
}

// openSplitMenu asks where to split s out to (tab/window, session only or
// with a sidebar; SPEC §6.6 asks every time).
func (m *Model) openSplitMenu(s *core.Session, y int) {
	if s.Kind != core.KindBackground {
		m.note = "Move it into acmd first (M)."
		return
	}
	split := func(window, side bool) func() tea.Cmd {
		return func() tea.Cmd {
			return m.do(func(ctx context.Context) error { return m.act.SplitOut(ctx, s, window, side) })
		}
	}
	items := []menuItem{
		{"New tab · session only", split(false, false)},
		{"New tab · with sidebar", split(false, true)},
		{"New window · session only", split(true, false)},
		{"New window · with sidebar", split(true, true)},
	}
	top := y + 1
	if top+len(items)+1 > m.h-footerMin {
		top = max(bodyTop, y-len(items)-1)
	}
	m.menu, m.mode = &menu{title: "Split out " + name(s), items: items, y: top + 1}, modeMenu
}

// cursorLine is the screen line of the cursor's row.
func (m *Model) cursorLine() int {
	for i, l := range m.bodyLines() {
		if l.key == m.cursor {
			return bodyTop + i - m.top
		}
	}
	return bodyTop
}
