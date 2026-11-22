package ipc

import (
	"slices"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

// The wire types: what clients see of a session, a group and the view. They
// follow the wire conventions (snake_case names, RFC 3339 times, values that
// aren't set left out, string ids) and are mapped from internal/core, so a
// change there can't change the protocol by accident. testdata/wire holds
// what they look like, and every client's tests decode it.

// Session is a session as clients see it.
type Session struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// GroupID is its group. Parent is the set it's in, else its group; Order
	// is its place in a set (from 1), 0 outside one.
	GroupID    string          `json:"group_id"`
	Parent     string          `json:"parent"`
	Order      int             `json:"order,omitempty"`
	NameSource core.NameSource `json:"name_source,omitempty"`
	Cwd        string          `json:"cwd"`
	// ClaudeSessionID is the agent's own conversation id, once known.
	ClaudeSessionID string      `json:"claude_session_id,omitempty"`
	ClaudeHistory   []string    `json:"claude_history,omitempty"`
	TranscriptPath  string      `json:"transcript_path,omitempty"`
	Status          core.Status `json:"status"`
	StatusDetail    string      `json:"status_detail,omitempty"`
	Unseen          bool        `json:"unseen,omitempty"`
	Kind            core.Kind   `json:"kind"`
	Harness         string      `json:"harness"`
	BgID            string      `json:"bg_id,omitempty"`
	Pid             int         `json:"pid,omitempty"`
	Remote          bool        `json:"remote,omitempty"`
	Host            string      `json:"host,omitempty"`
	RemoteID        string      `json:"remote_id,omitempty"`
	HostGone        bool        `json:"host_gone,omitempty"`
	JobID           string      `json:"job_id,omitempty"`
	External        *PaneLoc    `json:"external,omitempty"`
	CreatedAt       time.Time   `json:"created_at,omitzero"`
	LastEventAt     time.Time   `json:"last_event_at,omitzero"`
	StatusSince     time.Time   `json:"status_since,omitzero"`
	ArchivedAt      time.Time   `json:"archived_at,omitzero"`
	// Attention is why it needs you, if it does.
	Attention *Attention `json:"attention,omitempty"`
	// What it runs, its name to show, and what can be done with it (core/caps.go).
	Agent        string   `json:"agent,omitempty"`
	DisplayName  string   `json:"display_name,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Attention is why a session needs you, and how urgently: Rank 1 is the most
// urgent. Clients show a reason they don't know generically.
type Attention struct {
	Rank   int       `json:"rank"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since,omitzero"`
}

// PaneLoc is the terminal pane a session outside acmd's own terminals runs in.
type PaneLoc struct {
	Terminal      string `json:"terminal"`
	KittySocket   string `json:"kitty_socket,omitempty"`
	KittyWindow   int    `json:"kitty_window,omitempty"`
	GhosttyTerm   string `json:"ghostty_term,omitempty"`
	TTY           string `json:"tty,omitempty"`
	RestoreLayout string `json:"restore_layout,omitempty"`
	BarWindow     int    `json:"bar_window,omitempty"`
	HeaderWindow  int    `json:"header_window,omitempty"`
	HeaderTerm    string `json:"header_term,omitempty"`
	TermWindow    int    `json:"term_window,omitempty"`
	TermTerm      string `json:"term_term,omitempty"`
}

// Group is a group of sessions.
type Group struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "group"
	Name string `json:"name"`
	// Color is one of the color names (blue, magenta, cyan, yellow, green, red).
	Color string `json:"color,omitempty"`
	// Order is where you put it among the groups; Default has none, it's
	// always last.
	Order      int       `json:"order,omitempty"`
	IsDefault  bool      `json:"is_default,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitzero"`
	ArchivedAt time.Time `json:"archived_at,omitzero"`
	// Host is the Remote host a group is on, for a host's tree; "" for this
	// machine's.
	Host string `json:"host,omitempty"`
}

// ViewState is how the list is shown.
type ViewState struct {
	Mode         string          `json:"mode"` // "groups" | "list"
	Sort         string          `json:"sort"` // "created" | "name" | "activity" | "status" | "cwd"
	Desc         bool            `json:"desc,omitempty"`
	Filter       string          `json:"filter,omitempty"`
	FocusedGroup string          `json:"focused_group,omitempty"`
	PreFocus     map[string]bool `json:"pre_focus,omitempty"`
	ShowArchived bool            `json:"show_archived,omitempty"`
	// Collapsed are the collapsed groups' ids.
	Collapsed []string `json:"collapsed,omitempty"`
}

// SessionOf is s on the wire.
func SessionOf(s *core.Session) *Session {
	if s == nil {
		return nil
	}
	w := &Session{
		ID: s.ID, Name: s.Name, Description: s.Description, GroupID: s.GroupID, Parent: s.GroupID,
		NameSource: s.NameSource, Cwd: s.Cwd, ClaudeSessionID: s.ClaudeSessionID, ClaudeHistory: s.ClaudeHistory,
		TranscriptPath: s.TranscriptPath, Status: s.Status, StatusDetail: s.StatusDetail, Unseen: s.Unseen,
		Kind: s.Kind, Harness: s.Harness, BgID: s.BgID, Pid: s.Pid, Remote: s.Remote, Host: s.Host,
		RemoteID: s.RemoteID, HostGone: s.HostGone, JobID: s.JobID, External: paneOf(s.External),
		CreatedAt: s.CreatedAt, LastEventAt: s.LastEventAt, StatusSince: s.StatusSince, ArchivedAt: s.ArchivedAt,
		Agent: s.Agent(), DisplayName: s.DisplayName(), Capabilities: s.Capabilities(),
	}
	if s.SetID != "" {
		w.Parent, w.Order = s.SetID, s.SetOrder
	}
	if a := s.Attention(); a != nil {
		w.Attention = &Attention{Rank: a.Rank, Reason: a.Reason, Since: a.Since}
	}
	return w
}

// Core is the session back as internal/core has it, for the CLI's layout and
// actions.
func (w *Session) Core() *core.Session {
	if w == nil {
		return nil
	}
	s := &core.Session{
		ID: w.ID, Name: w.Name, Description: w.Description, GroupID: w.GroupID, NameSource: w.NameSource,
		Cwd: w.Cwd, ClaudeSessionID: w.ClaudeSessionID, ClaudeHistory: w.ClaudeHistory,
		TranscriptPath: w.TranscriptPath, Status: w.Status, StatusDetail: w.StatusDetail, Unseen: w.Unseen,
		Kind: w.Kind, Harness: w.Harness, BgID: w.BgID, Pid: w.Pid, Remote: w.Remote, Host: w.Host,
		RemoteID: w.RemoteID, HostGone: w.HostGone, JobID: w.JobID, External: w.External.Core(),
		CreatedAt: w.CreatedAt, LastEventAt: w.LastEventAt, StatusSince: w.StatusSince, ArchivedAt: w.ArchivedAt,
	}
	if w.Order > 0 {
		s.SetID, s.SetOrder = w.Parent, w.Order
	}
	return s
}

// SessionsOf is ss on the wire.
func SessionsOf(ss []*core.Session) []*Session {
	out := make([]*Session, 0, len(ss))
	for _, s := range ss {
		out = append(out, SessionOf(s))
	}
	return out
}

func paneOf(p *core.PaneLoc) *PaneLoc {
	if p == nil {
		return nil
	}
	return &PaneLoc{Terminal: p.Terminal, KittySocket: p.KittySocket, KittyWindow: p.KittyWindow,
		GhosttyTerm: p.GhosttyTerm, TTY: p.TTY, RestoreLayout: p.RestoreLayout, BarWindow: p.BarWindow,
		HeaderWindow: p.HeaderWindow, HeaderTerm: p.HeaderTerm, TermWindow: p.TermWindow, TermTerm: p.TermTerm}
}

// Core is the pane as internal/core has it.
func (p *PaneLoc) Core() *core.PaneLoc {
	if p == nil {
		return nil
	}
	return &core.PaneLoc{Terminal: p.Terminal, KittySocket: p.KittySocket, KittyWindow: p.KittyWindow,
		GhosttyTerm: p.GhosttyTerm, TTY: p.TTY, RestoreLayout: p.RestoreLayout, BarWindow: p.BarWindow,
		HeaderWindow: p.HeaderWindow, HeaderTerm: p.HeaderTerm, TermWindow: p.TermWindow, TermTerm: p.TermTerm}
}

// GroupOf is g on the wire.
func GroupOf(g *core.Group) *Group {
	w := &Group{ID: g.ID, Type: "group", Name: g.Name, Color: g.Color, IsDefault: g.IsDefault,
		CreatedAt: g.CreatedAt, ArchivedAt: g.ArchivedAt}
	if !g.IsDefault {
		w.Order = g.SortKey
	}
	return w
}

// GroupsOf is gs on the wire.
func GroupsOf(gs []*core.Group) []*Group {
	out := make([]*Group, 0, len(gs))
	for _, g := range gs {
		out = append(out, GroupOf(g))
	}
	return out
}

// Core is the group as internal/core has it; collapsed comes from the view.
func (g *Group) Core(collapsed bool) *core.Group {
	return &core.Group{ID: g.ID, Name: g.Name, Color: g.Color, SortKey: g.Order, IsDefault: g.IsDefault,
		CreatedAt: g.CreatedAt, ArchivedAt: g.ArchivedAt, Collapsed: collapsed}
}

// ViewStateOf is v on the wire.
func ViewStateOf(v core.ViewState) ViewState {
	return ViewState{Mode: v.Mode, Sort: v.Sort, Desc: v.Desc, Filter: v.Filter, FocusedGroup: v.FocusedGroup,
		PreFocus: v.PreFocus, ShowArchived: v.ShowArchived, Collapsed: v.Collapsed}
}

// Core is the view as internal/core has it.
func (v ViewState) Core() core.ViewState {
	return core.ViewState{Mode: v.Mode, Sort: v.Sort, Desc: v.Desc, Filter: v.Filter, FocusedGroup: v.FocusedGroup,
		PreFocus: v.PreFocus, ShowArchived: v.ShowArchived, Collapsed: v.Collapsed}
}

// CoreSessions are the snapshot's sessions as internal/core has them.
func (s Snapshot) CoreSessions() []*core.Session {
	out := make([]*core.Session, 0, len(s.Sessions))
	for _, w := range s.Sessions {
		out = append(out, w.Core())
	}
	return out
}

// CoreGroups are the snapshot's groups as internal/core has them, collapsed
// as the view says.
func (s Snapshot) CoreGroups() []*core.Group {
	collapsed := map[string]bool{}
	for _, id := range s.ViewState.Collapsed {
		collapsed[id] = true
	}
	out := make([]*core.Group, 0, len(s.Groups))
	for _, g := range s.Groups {
		out = append(out, g.Core(collapsed[g.ID]))
	}
	return out
}

// IsCollapsed says whether the group with id is collapsed.
func (v ViewState) IsCollapsed(id string) bool { return slices.Contains(v.Collapsed, id) }
