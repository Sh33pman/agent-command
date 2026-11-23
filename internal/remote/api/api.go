// Package api is what an authorized client may do on a host, over
// internal/remote/wire: the op names and their arguments and data. The host
// (internal/remote/hostapi) serves them, and the client daemon calls them, so
// both import the types from here.
//
// Every op but OpCall sees only the host's Remote sessions that aren't
// archived. A session that isn't Remote, or doesn't exist, is CodeNotFound
// either way, so a client can't tell the two apart.
//
// # Call
//
// OpCall runs one of the host daemon's own ops (internal/catalog), as a
// client on the host's socket would: Direct Connect, the whole host as the
// host has it. It takes any op a paired device may call: not one only for
// the host's own machine (HostOnly), not an admin op, not a stream, and not
// the host's own remote.* ops, so a host can't be used to reach its hosts.
// config.get and config.set are the host's settings, as OpConfigGet and
// OpConfigSet. A refused op is CodeForbidden.
//
// # Attach
//
// OpSessionAttach is a long-lived stream, in the mode its AttachRequest asks
// for: term.ModeStream, the only one now, and the one when it asks for none.
// A host refuses any other mode with errcode.NotSupported before the stream
// opens, so a client can ask for term.ModeRendered once there is one, and
// fall back. After its OK reply, the stream is a
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

import (
	"encoding/json"
	"time"

	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Version is the host API's version, in wire.Info's API. It's bumped on any
// change a client of the previous version couldn't follow; adding an op or a
// field isn't one.
const Version = 2 // 2: harnesses (harness_kind, harness_id, harnesses.list)

// MinVersion is the oldest host API this side still speaks.
const MinVersion = 2

// The ops.
const (
	OpSessionsList  = "sessions.list"  // no arguments → SessionList
	OpSessionAttach = "session.attach" // AttachRequest → a stream: term's protocol (see the package doc)
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
	// OpCall runs a host daemon op (see the package doc).
	OpCall = "call" // CallRequest → the op's answer
	// OpSubscribe is the host daemon's event stream, as on its socket: the
	// whole host (Direct Connect). ipc.SubscribeRequest → a stream of
	// ipc.Event lines.
	OpSubscribe = "subscribe"
)

// CallRequest names a daemon op and its argument, as the daemon's socket takes
// them (internal/ipc.Request): a change sent through mutate is Op "mutate"
// with the ipc.Mutation as Data.
type CallRequest struct {
	Op   string          `json:"op"`
	Data json.RawMessage `json:"data,omitempty"`
	// RequestID makes a creating op safe to retry, as ipc.Request's.
	RequestID string `json:"request_id,omitempty"`
	// Client is the UI on the paired machine that asked (its id, kind and
	// device), so the host records who did it, not only the pairing.
	Client *ipc.ClientHello `json:"client,omitempty"`
}

// Error codes the host API answers with, besides wire's: errcode's, as
// every transport has them.
const (
	CodeNotRunning = errcode.NotRunning
	CodePermission = errcode.PermissionDenied
	CodeExists     = errcode.Exists
	CodeNotFolder  = errcode.NotFolder
	CodeForbidden  = errcode.Forbidden
	CodeConflict   = errcode.Conflict
)

// Session is a Remote session, as a client sees it.
type Session struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HarnessKind string `json:"harness_kind"`         // "claude", "antigravity" or "terminal"
	HarnessID   string `json:"harness_id,omitempty"` // the host's harness it runs in
	Dir         string `json:"dir"`
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

// AttachRequest asks to attach to a session's terminal, in a mode (term's
// Mode*; "" is term.ModeStream).
type AttachRequest struct {
	ID   string `json:"id"`
	Mode string `json:"mode,omitempty"`
}

// NewSession starts a session on the host. It's always Remote.
type NewSession struct {
	Dir string `json:"dir"` // "~" and relative paths are from the host's home
	// HarnessID is one of the host's harnesses (harnesses.list). With none,
	// HarnessKind "terminal" is a terminal, and anything else the host's
	// default harness of that kind (or its default, for none).
	HarnessID   string   `json:"harness_id,omitempty"`
	HarnessKind string   `json:"harness_kind,omitempty"`
	Name        string   `json:"name,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Args        []string `json:"args,omitempty"` // after the host's own claude_args or agy_args
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
	// Files lists its Markdown and text files too, for choosing a prompt file;
	// with Exts, its files with those extensions (".json") instead.
	Files bool     `json:"files,omitempty"`
	Exts  []string `json:"exts,omitempty"`
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
	// Files are its Markdown and text files, with FolderPath.Files.
	Files []Folder `json:"files,omitempty"`
}

// Harness is one of the host's harnesses: what its sessions run in.
type Harness struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`  // "claude" or "antigravity"
	Title     string `json:"title"` // the kind's name: "Claude", "Antigravity"
	Installed bool   `json:"installed"`
}

// Harnesses are the host's harnesses, in its order, and the one a plain new
// session runs in ("" when none is installed). Terminal says a new session
// can be a terminal instead.
type Harnesses struct {
	Harnesses []Harness `json:"harnesses"`
	Default   string    `json:"default,omitempty"`
	Terminal  bool      `json:"terminal"`
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
