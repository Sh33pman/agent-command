// Package api is what an authorized client may do on a host, over
// internal/remote/wire: the op names and their arguments and data. The host
// (internal/remote/hostapi) serves them, and the client daemon calls them, so
// both import the types from here.
//
// Every op sees only the host's Remote sessions that aren't archived. A
// session that isn't Remote, or doesn't exist, is CodeNotFound either way, so
// a client can't tell the two apart.
//
// # Attach
//
// OpSessionAttach is a long-lived stream. After its OK reply, the stream is a
// byte pipe to the session's terminal (internal/term's server), as if the
// client had connected to its unix socket: the client writes term's frames,
// starting with the attach frame ('a': the protocol version, rows and cols),
// then input ('i') and resize ('r') frames; the host writes the terminal's
// output, starting with the replay of its screen. term.Attach, pointed at a
// local socket that relays to this stream, works unchanged.
//
// # Events
//
// OpEvents is a long-lived stream of Event lines: first EventSnapshot, then
// changes as they happen. A client that reads too slowly misses changes, and
// gets a fresh EventSnapshot in their place.
package api

import "time"

// Version is the host API's version, in wire.Info's API. It's bumped on any
// change a client of the previous version couldn't follow; adding an op or a
// field isn't one.
const Version = 1

// The ops.
const (
	OpSessionsList  = "sessions.list"  // no arguments → SessionList
	OpSessionAttach = "session.attach" // SessionRef → a stream: term's protocol (see the package doc)
	OpSessionNew    = "session.new"    // NewSession → Session
	OpSessionRename = "session.rename" // Rename → Session
	OpSessionEnd    = "session.end"    // End → no data
	OpFoldersRecent = "folders.recent" // no arguments → FolderList (Path "")
	OpFoldersList   = "folders.list"   // FolderPath → FolderList
	OpFoldersMkdir  = "folders.mkdir"  // FolderPath → Folder
	OpHarnessesList = "harnesses.list" // no arguments → Harnesses
	OpEvents        = "events"         // no arguments → a stream of Event lines
	// The host's settings, not its device ones (editor, speech).
	OpConfigGet = "config.get" // no arguments → ipc.ConfigState
	OpConfigSet = "config.set" // ipc.ConfigSet → ipc.ConfigState
)

// Error codes the host API adds to wire's (wire.CodeNotFound and
// wire.CodeBadRequest mean what they say there).
const (
	CodeNotRunning = "not_running"       // the session's terminal has ended: nothing to attach to
	CodePermission = "permission_denied" // the host's user may not read or write there
	CodeExists     = "exists"            // there's already something at that path
	CodeNotFolder  = "not_folder"        // the path is a file, not a folder
)

// Session is a Remote session, as a client sees it.
type Session struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Harness string `json:"harness"` // "claude", "antigravity" or "terminal"
	Dir     string `json:"dir"`
	// Status is the agent's state: core.Status ("busy", "waiting", "done",
	// "paused", …), and StatusDetail says more, when there's more to say.
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail,omitempty"`
	Unseen       bool   `json:"unseen,omitempty"`
	// NeedsYou counts the open inbox items that need an answer or a look:
	// questions, permissions and errors. Inbox counts every open item.
	NeedsYou     int       `json:"needs_you"`
	Inbox        int       `json:"inbox"`
	LastActivity time.Time `json:"last_activity"`
	Created      time.Time `json:"created"`
}

// SessionList is the Remote sessions, the most recently active first.
type SessionList struct {
	Sessions []Session `json:"sessions"`
}

// SessionRef names a session.
type SessionRef struct {
	ID string `json:"id"`
}

// NewSession starts a session on the host. It's always Remote.
type NewSession struct {
	Dir     string   `json:"dir"`               // "~" and relative paths are from the host's home
	Harness string   `json:"harness,omitempty"` // "" = the host's default
	Name    string   `json:"name,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Args    []string `json:"args,omitempty"` // after the host's own claude_args or agy_args
}

// Rename names a session.
type Rename struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// End ends a session as the app does: it's archived, and its terminal ends.
// Finish instead stops its agent and keeps it listed, as `acmd finish` does.
type End struct {
	ID     string `json:"id"`
	Finish bool   `json:"finish,omitempty"`
}

// FolderPath is a folder on the host: absolute, or from the host's home ("~",
// "~/src" or "src"). "" is the home folder.
type FolderPath struct {
	Path string `json:"path"`
	// Hidden lists hidden subfolders (".git") too, flagged Hidden; folders.list
	// leaves them out otherwise.
	Hidden bool `json:"hidden,omitempty"`
}

// Folder is a folder on the host.
type Folder struct {
	Name   string `json:"name"`
	Path   string `json:"path"` // absolute
	Hidden bool   `json:"hidden,omitempty"`
}

// FolderList is a folder's subfolders, by name, hidden ones last. For
// folders.recent, it's the folders of recent sessions, the most recent first.
type FolderList struct {
	Path    string   `json:"path,omitempty"` // the folder listed, absolute
	Parent  string   `json:"parent,omitempty"`
	Home    string   `json:"home"` // the host's home folder
	Folders []Folder `json:"folders"`
}

// Harness is something the host can run in a session.
type Harness struct {
	Name  string `json:"name"`  // "claude", "antigravity" or "terminal"
	Title string `json:"title"` // what to call it: "Claude Code"
}

// Harnesses are what's installed on the host, and its default ("" when the
// host's user hasn't chosen one: clients should ask).
type Harnesses struct {
	Harnesses []Harness `json:"harnesses"`
	Default   string    `json:"default,omitempty"`
}

// Event types.
const (
	EventSnapshot = "snapshot" // Sessions and Inbox: everything, replacing what the client had
	EventAdded    = "added"    // Session: newly Remote, or new
	EventChanged  = "changed"  // Session: its name, agent state, counts or activity changed
	EventRemoved  = "removed"  // ID: no longer Remote, archived or gone
	EventInbox    = "inbox"    // ID and Inbox: that session's open items now
	EventNotice   = "notice"   // Notice: a notification about a Remote session
)

// Event is one line of OpEvents.
type Event struct {
	Type     string      `json:"type"`
	Sessions []Session   `json:"sessions,omitempty"`
	Session  *Session    `json:"session,omitempty"`
	ID       string      `json:"id,omitempty"`
	Inbox    []InboxItem `json:"inbox,omitempty"`
	Notice   *Notice     `json:"notice,omitempty"`
}

// InboxItem is an open item in a Remote session's inbox: a question, a
// permission, an error or a finished turn.
type InboxItem struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Options   []string  `json:"options,omitempty"`
	Multipart bool      `json:"multipart,omitempty"`
	At        time.Time `json:"at"`
	Read      bool      `json:"read,omitempty"`
}

// Notice is a notification the host decided on for a Remote session.
type Notice struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"` // waiting | error | done | paused | withdraw
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Urgent    bool   `json:"urgent,omitempty"`
}
