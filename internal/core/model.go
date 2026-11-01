// Package core holds acmd's session model and the status state machine
// (SPEC §5). It does no I/O: callers feed it events from hooks, the
// `claude agents --json` reconciler and transcript tailing, and persist the
// result.
package core

import "time"

// Status is a session's user-visible state (SPEC §5.2).
type Status string

// Statuses, in SPEC §5.2 order.
const (
	StatusStarting Status = "starting"
	StatusBusy     Status = "busy"
	StatusWaiting  Status = "waiting"
	StatusDone     Status = "done"
	StatusError    Status = "error"
	StatusPaused   Status = "paused"
	// StatusFinished: the user finished the session (claude stop); its
	// conversation is kept and showing it resumes it (SPEC §5.2).
	StatusFinished Status = "finished"
	StatusArchived Status = "archived"
)

// NeedsAttention reports whether reaching this status should mark a session
// unseen and may notify (SPEC §5.2, §8.5).
func (s Status) NeedsAttention() bool {
	return s == StatusWaiting || s == StatusDone || s == StatusError
}

// Kind says where a session runs (SPEC §5.1).
type Kind string

// Session kinds.
const (
	// KindBackground is a Claude background session (`claude --bg`), viewable in acmd.
	KindBackground Kind = "background"
	// KindExternal is an interactive `claude` in one of the user's own panes.
	KindExternal Kind = "external"
)

// NameSource says whether a session's name may still be replaced automatically.
type NameSource string

// Name sources.
const (
	NameAuto NameSource = "auto"
	NameUser NameSource = "user"
)

// PaneLoc is a pane in the user's terminal (SPEC §5.1).
type PaneLoc struct {
	Terminal    string // "kitty" | "ghostty" | "unknown"
	KittySocket string
	KittyWindow int
	GhosttyTerm string
	TTY         string
	// RestoreLayout is the tab layout to restore when acmd removes this
	// pane (a view's sidebar: the layout the tab had before).
	RestoreLayout string `json:",omitempty"`
	// BarWindow is the Kitty window of a view's full-width status bar
	// (set on a view's sidebar; 0 = none, the sidebar shows messages).
	BarWindow int `json:",omitempty"`
	// HeaderWindow / HeaderTerm: the header bar above a view pane (set on
	// a view's sidebar; Kitty window id / Ghostty terminal id).
	HeaderWindow int    `json:",omitempty"`
	HeaderTerm   string `json:",omitempty"`
	// TermWindow / TermTerm: the terminal pane under a view pane (set on a
	// view's sidebar; SPEC §8.7). It may have closed by itself since.
	TermWindow int    `json:",omitempty"`
	TermTerm   string `json:",omitempty"`
}

// Session is an acmd session (SPEC §5.1). Its ID is stable while Claude's
// session id changes on /clear, forks and moves into the background.
type Session struct {
	ID              string
	Name            string
	Description     string
	GroupID         string
	NameSource      NameSource
	Cwd             string
	ClaudeSessionID string
	ClaudeHistory   []string
	TranscriptPath  string
	Status          Status
	StatusDetail    string
	Unseen          bool
	Kind            Kind
	BgID            string
	Pid             int
	External        *PaneLoc
	CreatedAt       time.Time
	LastEventAt     time.Time
	ArchivedAt      time.Time
}

// Group is a session group (SPEC §8.4).
type Group struct {
	ID         string
	Name       string
	Color      string
	SortKey    int
	Collapsed  bool
	IsDefault  bool
	CreatedAt  time.Time
	ArchivedAt time.Time
}

// View is a pane where the user ran `acmd` (SPEC §6.4).
type View struct {
	ID            string
	Pane          PaneLoc
	Sidebar       PaneLoc
	Selected      string
	SidebarHidden bool
	LastFocusedAt time.Time
}

// ViewState is the list presentation shared by every sidebar (SPEC §8.4).
type ViewState struct {
	Mode         string // "groups" | "list"
	Sort         string // "created" | "name" | "activity" | "status" | "cwd"
	Desc         bool
	Filter       string
	FocusedGroup string
	PreFocus     map[string]bool
	ShowArchived bool
}

// NeedsResume reports whether a view must resume s (claude --bg --resume)
// before it can show it: a stopped background session acmd never got a
// Claude background id for, with a conversation to resume.
func (s *Session) NeedsResume() bool {
	return s.Kind == KindBackground && s.BgID == "" && s.Stopped() && s.ClaudeSessionID != ""
}

// Stopped reports whether s's Claude process isn't running by design:
// paused (reboot, idle stop, claude stop) or finished by the user.
func (s *Session) Stopped() bool { return s.Status == StatusPaused || s.Status == StatusFinished }
