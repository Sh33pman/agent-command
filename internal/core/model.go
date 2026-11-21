// Package core holds acmd's session model and the status state machine.
// It does no I/O: callers feed it events from hooks, the
// `claude agents --json` reconciler and transcript tailing, and persist the
// result.
package core

import (
	"encoding/json"
	"slices"
	"time"
)

// Status is a session's user-visible state.
type Status string

// Statuses.
const (
	StatusStarting Status = "starting"
	StatusBusy     Status = "busy"
	StatusWaiting  Status = "waiting"
	StatusDone     Status = "done"
	StatusError    Status = "error"
	StatusPaused   Status = "paused"
	// StatusRunning: a terminal whose shell runs; it has no finer status.
	StatusRunning Status = "running"
	// StatusUnknown: an agent runs in a terminal but sends no status events
	// (its hooks aren't installed, or acmd doesn't know it). StatusDetail names it.
	StatusUnknown Status = "unknown"
	// StatusFinished: the user finished the session (claude stop); its
	// conversation is kept and showing it resumes it.
	StatusFinished Status = "finished"
	StatusArchived Status = "archived"
)

// NeedsAttention reports whether reaching this status should mark a session
// unseen and may notify.
func (s Status) NeedsAttention() bool {
	return s == StatusWaiting || s == StatusDone || s == StatusError
}

// Kind says where a session runs.
type Kind string

// Session kinds.
const (
	// KindBackground is a Claude background session (`claude --bg`), viewable in acmd.
	KindBackground Kind = "background"
	// KindHosted is an interactive CLI that acmd hosts under its own PTY,
	// viewable in acmd. It replaces KindBackground for new sessions.
	KindHosted Kind = "hosted"
	// KindExternal is an interactive `claude` in one of the user's own panes.
	KindExternal Kind = "external"
)

// What a session runs.
const (
	HarnessClaude   = "claude"
	HarnessTerminal = "terminal"
	// HarnessAntigravity is Google's Antigravity CLI (agy). Its conversation id
	// is kept in ClaudeSessionID too.
	HarnessAntigravity = "antigravity"
)

// AgentName is how messages name the agent a harness runs: "Claude" (also
// for a terminal, whose agent is usually Claude) or "Antigravity".
func AgentName(harness string) string {
	if harness == HarnessAntigravity {
		return antigravityAgent
	}
	return "Claude"
}

// AgentApp is how messages name the program a harness runs, as in "the
// installed Claude Code".
func AgentApp(harness string) string {
	if harness == HarnessAntigravity {
		return "Antigravity CLI"
	}
	return "Claude Code"
}

// NameSource says whether a session's name may still be replaced automatically.
type NameSource string

// Name sources.
const (
	NameAuto NameSource = "auto"
	NameUser NameSource = "user"
)

// PaneLoc is a pane in the user's terminal.
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
	// view's sidebar). It may have closed by itself since.
	TermWindow int    `json:",omitempty"`
	TermTerm   string `json:",omitempty"`
}

// Session is an acmd session. Its ID is stable while Claude's
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
	Harness         string // HarnessClaude, HarnessTerminal or HarnessAntigravity
	BgID            string
	Pid             int
	// Remote: clients of this acmd's Hosting may see and use it (Remote sessions).
	Remote bool
	// Host is the Remote host a session added from one runs on ("" for a
	// session on this machine), and RemoteID its id there. HostGone: the host
	// no longer offers it (ended there, or no longer Remote), so it can only
	// be removed.
	Host     string
	RemoteID string
	HostGone bool
	// JobID is the scheduled job that started it ("" if none did). A job that
	// continues an existing session leaves it as it was.
	JobID       string
	External    *PaneLoc
	CreatedAt   time.Time
	LastEventAt time.Time
	ArchivedAt  time.Time
	// StatusSince is when Status last changed. The store keeps it.
	StatusSince time.Time `json:"-"`
	// SetID is the set it's in ("" for none), and SetOrder its place there
	// (from 1). Only the store's set functions change them.
	SetID    string `json:"-"`
	SetOrder int    `json:"-"`
	// Parent and Order are how clients see the above (section 7 of the
	// design): the set it's in, else its group, and its place in a set.
	// MarshalJSON fills them in; nothing else sets them.
	Parent string `json:"parent,omitempty"`
	Order  int    `json:"order,omitempty"`
	// Needs is Attention(), for clients. MarshalJSON fills it in.
	Needs *Attention `json:"attention,omitempty"`
	// What it is and what can be done with it, for clients (caps.go).
	// MarshalJSON fills them in.
	AgentNow string   `json:"agent,omitempty"`
	Display  string   `json:"display_name,omitempty"`
	Can      []string `json:"capabilities,omitempty"`
}

// MarshalJSON writes the session with Parent, Order and Needs filled in.
func (s Session) MarshalJSON() ([]byte, error) {
	type plain Session // without this method
	p := plain(s)
	p.Parent, p.Order = s.GroupID, 0
	if s.SetID != "" {
		p.Parent, p.Order = s.SetID, s.SetOrder
	}
	p.Needs = s.Attention()
	p.AgentNow, p.Display, p.Can = s.Agent(), s.DisplayName(), s.Capabilities()
	return json.Marshal(p)
}

// Attention is why a session needs you, and how urgently: Rank 1 is the
// most urgent. Reasons can be added; clients show one they don't know
// generically, and still order it by Rank.
type Attention struct {
	Rank   int       `json:"rank"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"` // when it started needing you
}

// Attention reasons.
const (
	NeedsWaiting    = "waiting"     // it asks you something
	NeedsError      = "error"       // it stopped on an error
	NeedsUnseenDone = "unseen_done" // it finished a turn you haven't looked at
)

// Attention says whether s needs you; nil when it doesn't.
func (s *Session) Attention() *Attention {
	since := s.StatusSince
	if since.IsZero() {
		since = s.LastEventAt
	}
	switch {
	case s.Status == StatusWaiting:
		return &Attention{Rank: 1, Reason: NeedsWaiting, Since: since}
	case s.Status == StatusError:
		return &Attention{Rank: 2, Reason: NeedsError, Since: since}
	case s.Status == StatusDone && s.Unseen:
		return &Attention{Rank: 3, Reason: NeedsUnseenDone, Since: since}
	}
	return nil
}

// AttentionBefore reports whether a should come before b in "next that needs
// you": the lower rank, then the one waiting longest, then by id. Sessions
// that don't need you come last.
func AttentionBefore(a, b *Session) bool {
	x, y := a.Attention(), b.Attention()
	switch {
	case x == nil || y == nil:
		return x != nil && y == nil
	case x.Rank != y.Rank:
		return x.Rank < y.Rank
	case !x.Since.Equal(y.Since):
		return x.Since.Before(y.Since)
	}
	return a.ID < b.ID
}

// Set is sessions made together by splitting panes. Its group and host are
// its sessions': they are all in one group, on one host.
type Set struct {
	ID        string
	GroupID   string
	Host      string // "" for this machine
	CreatedAt time.Time
}

// Group is a session group.
type Group struct {
	ID      string
	Name    string
	Color   string
	SortKey int
	// Collapsed is the CLI's view of the group; clients get it in
	// ViewState.Collapsed, not with the group.
	Collapsed  bool `json:"-"`
	IsDefault  bool
	CreatedAt  time.Time
	ArchivedAt time.Time
}

// MarshalJSON writes the group with its type and, for one you arranged, its
// order (section 7 of the design). Default has none: it always comes last.
func (g Group) MarshalJSON() ([]byte, error) {
	type plain Group // without this method
	out := struct {
		plain
		Type  string `json:"type"`
		Order int    `json:"order,omitempty"`
	}{plain: plain(g), Type: "group"}
	if !g.IsDefault {
		out.Order = g.SortKey
	}
	return json.Marshal(out)
}

// ViewState is the CLI's list presentation, kept by the daemon. Each other
// client keeps its own view.
type ViewState struct {
	Mode         string // "groups" | "list"
	Sort         string // "created" | "name" | "activity" | "status" | "cwd"
	Desc         bool
	Filter       string
	FocusedGroup string
	PreFocus     map[string]bool
	ShowArchived bool
	// Collapsed are the collapsed groups' ids. The daemon fills it in from
	// its groups when it sends the view.
	Collapsed []string `json:",omitempty"`
}

// IsCollapsed reports whether group id is collapsed in this view.
func (vs ViewState) IsCollapsed(id string) bool { return slices.Contains(vs.Collapsed, id) }

// Managed reports whether acmd runs s, so a view can show it: a background
// or a hosted session (not one in the user's own pane).
func (s *Session) Managed() bool { return s.Kind == KindBackground || s.Kind == KindHosted }

// IsTerminal reports whether s is a terminal session. It may have an agent
// running in it, typed by the user, whose status it then shows.
func (s *Session) IsTerminal() bool { return s.Harness == HarnessTerminal }

// IsAntigravity reports whether s runs Antigravity's CLI. It can't be told its
// conversation id up front, and Claude's listing never shows it.
func (s *Session) IsAntigravity() bool { return s.Harness == HarnessAntigravity }

// TakeAgentEvent reports whether a terminal should apply an event from an
// agent running in it, and adopts that agent if it has none. The last agent to
// start wins, so a second one in the same terminal can't confuse the first.
func (s *Session) TakeAgentEvent(e Event, pid int) bool {
	if s.Status == StatusArchived || s.Stopped() || (!e.Time.IsZero() && e.Time.Before(s.LastEventAt)) {
		return false
	}
	switch {
	case s.ClaudeSessionID == "":
		if e.Kind == HookSessionEnd {
			return false
		}
		s.ClaudeSessionID = e.SessionID
	case e.SessionID != s.ClaudeSessionID && e.Kind != HookSessionStart:
		return false
	}
	if pid > 0 {
		s.Pid = pid
	}
	return true
}

// FollowConversation reports whether a hosted session should apply an event from
// the agent it runs, and moves it to that event's conversation if it's a new one.
// Claude changes its session id on /clear and the like, and the session stays the
// same. A late event from an earlier conversation is dropped.
func (s *Session) FollowConversation(e Event) bool {
	if e.SessionID == "" || e.SessionID == s.ClaudeSessionID {
		return true
	}
	if slices.Contains(s.ClaudeHistory, e.SessionID) {
		return false
	}
	switch e.Kind {
	case HookSessionStart:
		return true // Apply adopts its id
	case HookSessionEnd:
		return false
	}
	if s.ClaudeSessionID != "" {
		s.ClaudeHistory = append(s.ClaudeHistory, s.ClaudeSessionID)
	}
	s.ClaudeSessionID = e.SessionID
	return true
}

// EndAgent returns a terminal to its own status once its agent has gone. The
// conversation isn't the terminal's, so nothing of it stays.
func (s *Session) EndAgent() {
	s.Status, s.StatusDetail = StatusRunning, ""
	s.ClaudeSessionID, s.ClaudeHistory, s.TranscriptPath, s.Pid = "", nil, "", 0
	if s.NameSource == NameAuto {
		s.Name, s.NameSource = "", ""
	}
}

// NeedsResume reports whether a view must resume s before it can show it:
// a stopped hosted session (its host is gone), or a session from before
// hosting (`claude --bg`), which resume turns into a hosted one; either
// with a conversation to resume.
func (s *Session) NeedsResume() bool {
	if s.ClaudeSessionID == "" {
		return false
	}
	return (s.Kind == KindHosted && s.Stopped()) || s.Kind == KindBackground
}

// Stopped reports whether s's Claude process isn't running by design:
// paused (reboot, idle stop, claude stop) or finished by the user.
// OnHost reports whether s runs on a Remote host: added here from one, and
// used through it. Nothing on this machine runs it.
func (s *Session) OnHost() bool { return s.Host != "" }

func (s *Session) Stopped() bool { return s.Status == StatusPaused || s.Status == StatusFinished }

// Ref names s on a command line (`acmd go <ref>`): the Claude background
// id when it has one, else its acmd id.
func (s *Session) Ref() string {
	if s.BgID != "" {
		return s.BgID
	}
	return s.ID
}
