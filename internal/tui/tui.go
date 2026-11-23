// Package tui is acmd's terminal UI: a full-terminal list of the sessions
// here and on your hosts, grouped as in the app's sidebar, to attach to,
// answer and start from the keyboard, in any terminal (macOS, Linux and
// Windows).
//
// It's a client like any other: it talks to the daemon only through its ops
// and its event stream (Daemon, Stream), and imports nothing of the daemon's
// own (a test keeps it that way), so it can move into a binary of its own.
package tui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Daemon runs the daemon's ops.
type Daemon interface {
	Call(op string, in, out any) error
}

// Stream is the daemon's event stream.
type Stream interface {
	NextEvent() (ipc.Event, error)
}

// Options are what the terminal UI runs with.
type Options struct {
	Daemon Daemon
	// Subscribe opens the event stream, on a connection of its own.
	Subscribe func() (Stream, error)
	// Attach attaches this terminal to a session: it runs with the terminal
	// handed over, and the list comes back when it returns (Ctrl+]).
	Attach func(s ipc.Session) tea.ExecCommand
	// Me is this terminal client's id: the notices sent to it ring the bell.
	Me string
	// Now is the clock (time.Now).
	Now func() time.Time
}

// Run runs the terminal UI on in and out until it's quit.
func Run(o Options, in io.Reader, out io.Writer) error {
	_, err := tea.NewProgram(New(o), tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}

// mode is what the keys do now.
type mode int

const (
	modeList   mode = iota
	modeFilter      // typing the filter
	modeAnswer      // picking an option for the selected session's prompt
	modeReply       // typing a reply to its finished turn
	modeNew         // typing the folder to start a session in
)

// refreshEvery is the most often the snapshot is read again as events come.
const refreshEvery = 250 * time.Millisecond

// Model is the terminal UI's state.
type Model struct {
	o      Options
	snap   ipc.Snapshot
	loaded bool
	rows   []row
	sel    int // index into rows; always a session row when there is one
	width  int
	height int

	mode   mode
	filter string
	input  string
	item   *core.InboxItem // the item modeAnswer or modeReply answers
	newOn  string          // the host modeNew starts on ("" for here)

	// status is the line above the keys: the last notice, or what went wrong.
	status string

	loading, dirty bool // a snapshot read is on its way; another is wanted after it

	// exec hands the terminal over to an attach (tea.Exec).
	exec func(tea.ExecCommand, tea.ExecCallback) tea.Cmd
}

// row is one line of the list: a section (this machine or a host), a group,
// or a session.
type row struct {
	section string // a section header: "" for others
	group   string // a group header: "" for others
	host    string // the section's host ("" for this machine)
	s       *core.Session
	waiting *core.InboxItem // its open Inbox item, if any
}

func (r row) isSession() bool { return r.s != nil }

// New is the terminal UI's model, before anything is loaded.
func New(o Options) Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	return Model{o: o, exec: tea.Exec}
}

// Messages.
type (
	snapshotMsg struct {
		snap ipc.Snapshot
		err  error
	}
	streamMsg struct {
		stream Stream
		err    error
	}
	eventMsg struct {
		stream Stream
		ev     ipc.Event
		err    error
	}
	refreshMsg  struct{}
	doneMsg     struct{ text string }
	failedMsg   struct{ err error }
	attachedMsg struct{ err error }
)

// Init reads the snapshot and opens the event stream.
func (m Model) Init() tea.Cmd { return tea.Batch(m.load(), m.subscribe()) }

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		var s ipc.Snapshot
		err := m.o.Daemon.Call(ipc.OpSnapshot, nil, &s)
		return snapshotMsg{s, err}
	}
}

func (m Model) subscribe() tea.Cmd {
	if m.o.Subscribe == nil {
		return nil
	}
	return func() tea.Msg {
		s, err := m.o.Subscribe()
		return streamMsg{s, err}
	}
}

func next(s Stream) tea.Cmd {
	return func() tea.Msg {
		ev, err := s.NextEvent()
		return eventMsg{s, ev, err}
	}
}

// call runs op off the UI's goroutine, saying what it did or what went wrong.
func (m Model) call(op string, in any, did string) tea.Cmd {
	return func() tea.Msg {
		if err := m.o.Daemon.Call(op, in, nil); err != nil {
			return failedMsg{err}
		}
		return doneMsg{did}
	}
}

// Update applies a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case snapshotMsg:
		m.loading = false
		if msg.err != nil {
			m.status = "✗ " + msg.err.Error()
			return m, nil
		}
		m.snap, m.loaded = msg.snap, true
		m.build()
		if m.dirty {
			m.dirty = false
			return m.refresh()
		}
	case streamMsg:
		if msg.err != nil {
			m.status = "✗ no live updates: " + msg.err.Error()
			return m, nil
		}
		return m, next(msg.stream)
	case eventMsg:
		if msg.err != nil {
			m.status = "✗ live updates stopped: " + msg.err.Error()
			return m, nil
		}
		var cmds []tea.Cmd
		cmds = append(cmds, next(msg.stream))
		if msg.ev.Entity == ipc.EntityNotice && msg.ev.Type == ipc.EventAdded {
			if c := m.notice(msg.ev); c != nil {
				cmds = append(cmds, c)
			}
		} else if msg.ev.Type != ipc.EventSnapshot {
			var c tea.Cmd
			m, c = m.refresh()
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	case refreshMsg:
		return m, m.load()
	case doneMsg:
		m.status = msg.text
		return m.refresh()
	case failedMsg:
		m.status = "✗ " + msg.err.Error()
	case attachedMsg:
		if msg.err != nil {
			m.status = "✗ " + msg.err.Error()
		}
		return m.refresh()
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

// refresh reads the snapshot again soon: at most every refreshEvery, however
// many events come.
func (m Model) refresh() (Model, tea.Cmd) {
	if m.loading {
		m.dirty = true
		return m, nil
	}
	m.loading = true
	return m, tea.Tick(refreshEvery, func(time.Time) tea.Msg { return refreshMsg{} })
}

// notice shows a notice on the status line, and rings the bell for one sent
// to this terminal.
func (m *Model) notice(ev ipc.Event) tea.Cmd {
	var n ipc.Notice
	if json.Unmarshal(ev.Data, &n) != nil || n.Kind == "withdraw" {
		return nil
	}
	text := cmp.Or(n.Title, n.Kind)
	if n.Body != "" {
		text += ": " + n.Body
	}
	if n.Host != "" {
		text += " (on " + n.Host + ")"
	}
	m.status = m.o.Now().Format("15:04") + " " + text
	if slices.Contains(n.To, m.o.Me) {
		return tea.Raw("\a")
	}
	return nil
}

// build lays the rows out from the snapshot: this machine's sessions, then
// each host's, each in its groups, with what's waiting.
func (m *Model) build() {
	var keep *core.Session
	if s := m.selected(); s != nil {
		keep = s
	}
	sessions, groups := m.snap.CoreSessions(), m.snap.CoreGroups()
	waiting := map[[2]string]*core.InboxItem{}
	for i := range m.snap.Inbox {
		it := &m.snap.Inbox[i]
		if it.State == "" || it.State == core.InboxOpen {
			k := [2]string{it.Host, it.SessionID}
			if waiting[k] == nil {
				waiting[k] = it
			}
		}
	}
	var hosts []string
	for _, h := range m.snap.RemoteHosts {
		hosts = append(hosts, h.Name)
	}
	for _, s := range sessions {
		if s.Host != "" && !slices.Contains(hosts, s.Host) {
			hosts = append(hosts, s.Host)
		}
	}
	m.rows = nil
	for _, host := range append([]string{""}, hosts...) {
		var hs []*core.Session
		for _, s := range sessions {
			if s.Host == host {
				hs = append(hs, s)
			}
		}
		var hg []*core.Group
		for _, g := range groups {
			if g.Host == host {
				cp := *g
				cp.Collapsed = false // the list shows every session
				hg = append(hg, &cp)
			}
		}
		// Grouped as in the sidebar, every group open; this machine's sort is
		// its view's.
		vs := core.ViewState{Mode: "groups", Filter: m.filter}
		if host == "" {
			vs.Sort, vs.Desc = m.snap.ViewState.Sort, m.snap.ViewState.Desc
		}
		laid := core.Layout(hs, hg, vs)
		title := "This machine"
		if host != "" {
			title = host + m.hostState(host)
		}
		m.rows = append(m.rows, row{section: title, host: host})
		for _, r := range laid {
			if r.IsHeader() {
				if r.Count == 0 {
					continue
				}
				m.rows = append(m.rows, row{group: fmt.Sprintf("%s (%d)", r.Group.Name, r.Count), host: host})
				continue
			}
			m.rows = append(m.rows, row{host: host, s: r.Session, waiting: waiting[[2]string{host, r.Session.ID}]})
		}
	}
	m.sel = m.find(keep)
}

// hostState is " (unreachable)" and the like for a host that isn't connected.
func (m Model) hostState(name string) string {
	for _, h := range m.snap.RemoteHosts {
		if h.Name == name && h.State != ipc.HostConnected && h.State != "" {
			return " (" + h.State + ")"
		}
	}
	return ""
}

// find is the row of s, else the first session row, else 0.
func (m Model) find(s *core.Session) int {
	first := -1
	for i, r := range m.rows {
		if !r.isSession() {
			continue
		}
		if first < 0 {
			first = i
		}
		if s != nil && r.s.ID == s.ID && r.s.Host == s.Host {
			return i
		}
	}
	return max(first, 0)
}

// selected is the selected session, if there is one.
func (m Model) selected() *core.Session {
	if m.sel < len(m.rows) && m.rows[m.sel].isSession() {
		return m.rows[m.sel].s
	}
	return nil
}

// move selects the next session row in dir (+1 or -1), if there is one.
func (m *Model) move(dir int) {
	for i := m.sel + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].isSession() {
			m.sel = i
			return
		}
	}
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeFilter:
		return m.typed(k, func(m Model) (Model, tea.Cmd) { m.mode = modeList; return m, nil }, func(m *Model) {
			m.filter = m.input
			m.build()
		})
	case modeReply:
		return m.typed(k, func(m Model) (Model, tea.Cmd) {
			m.mode = modeList
			it := m.item
			return m, m.call(ipc.OpInboxAnswer, ipc.InboxAnswer{Host: it.Host, ID: it.ID, Text: m.input}, "Replied to "+m.name(it))
		}, nil)
	case modeNew:
		return m.typed(k, func(m Model) (Model, tea.Cmd) {
			m.mode = modeList
			dir := strings.TrimSpace(m.input)
			if m.newOn != "" {
				return m, m.call(ipc.OpRemoteNew, ipc.RemoteNewRequest{Host: m.newOn, Dir: cmp.Or(dir, "~")}, "Started a session on "+m.newOn)
			}
			if dir == "" {
				m.status = "✗ a session here needs a folder"
				return m, nil
			}
			return m, m.call(ipc.OpNew, ipc.NewRequest{Cwd: dir}, "Started a session in "+dir)
		}, nil)
	case modeAnswer:
		if key == "esc" {
			m.mode, m.status = modeList, ""
			return m, nil
		}
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			n := int(key[0] - '0')
			if n <= len(m.item.Options) {
				m.mode = modeList
				it := m.item
				return m, m.call(ipc.OpInboxAnswer, ipc.InboxAnswer{Host: it.Host, ID: it.ID, Option: n},
					fmt.Sprintf("Answered %s: %s", m.name(it), it.Options[n-1]))
			}
		}
		return m, nil
	}
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(+1)
	case "/":
		m.mode, m.input = modeFilter, m.filter
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.build()
		}
	case "enter":
		s := m.selected()
		if s == nil || m.o.Attach == nil {
			return m, nil
		}
		return m, m.exec(m.o.Attach(*ipc.SessionOf(s)), func(err error) tea.Msg { return attachedMsg{err} })
	case "a":
		return m.answer()
	case "n":
		m.mode, m.input = modeNew, ""
		m.newOn = m.newHost()
		if m.newOn == "" && !m.sessionsHere() {
			m.mode, m.status = modeList, "✗ this machine runs no sessions, and has no host to start one on: acmd remote add"
		}
	}
	return m, nil
}

// typed edits m.input for a key: Enter calls done, Esc cancels, and changed
// runs after each edit.
func (m Model) typed(k tea.KeyPressMsg, done func(Model) (Model, tea.Cmd), changed func(*Model)) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		return done(m)
	case "esc":
		m.mode, m.input = modeList, ""
		if changed != nil {
			changed(&m)
		}
		return m, nil
	case "backspace":
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
	default:
		if k.Text == "" {
			return m, nil
		}
		m.input += k.Text
	}
	if changed != nil {
		changed(&m)
	}
	return m, nil
}

// answer answers the selected session's Inbox item: an option for a prompt or
// a question, else a reply.
func (m Model) answer() (tea.Model, tea.Cmd) {
	if m.sel >= len(m.rows) || m.rows[m.sel].waiting == nil {
		m.status = "Nothing is waiting there."
		return m, nil
	}
	it := m.rows[m.sel].waiting
	m.item = it
	switch {
	case it.Multipart:
		m.status = "It has several parts: press Enter, and answer it in the session."
	case len(it.Options) > 0:
		m.mode = modeAnswer
	default:
		m.mode, m.input = modeReply, ""
	}
	return m, nil
}

// sessionsHere says whether this machine runs sessions (a client-only one,
// Windows, doesn't).
func (m Model) sessionsHere() bool {
	return m.snap.Features == nil || m.snap.Features.Host.Sessions
}

// newHost is where n starts a session: the selected session's host, else
// here, else (on a machine that runs none) the first connected host.
func (m Model) newHost() string {
	if m.sel < len(m.rows) && m.rows[m.sel].host != "" {
		return m.rows[m.sel].host
	}
	if m.sessionsHere() {
		return ""
	}
	for _, h := range m.snap.RemoteHosts {
		if h.State == ipc.HostConnected {
			return h.Name
		}
	}
	return ""
}

// name names an item's session, for the status line.
func (m Model) name(it *core.InboxItem) string {
	for _, r := range m.rows {
		if r.isSession() && r.s.ID == it.SessionID && r.s.Host == it.Host {
			return sessionName(r.s)
		}
	}
	return "session " + it.SessionID
}

func sessionName(s *core.Session) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Cwd != "" {
		parts := strings.FieldsFunc(s.Cwd, func(r rune) bool { return r == '/' || r == '\\' })
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return "session " + s.Ref()
}
