// Package ipc is acmd's local protocol: newline-delimited JSON over the
// daemon's unix socket. Every connection starts with a hello
// that carries the protocol and binary versions, so a CLI or hook can
// replace a daemon left over from before an upgrade.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/schedule"
)

// Proto is the newest protocol version this build speaks, and ProtoMin the
// oldest (design 8.2, D6): a client and its daemon use the highest version
// both speak, and decide what to show from the daemon's ops, never from the
// version. Proto is bumped only on incompatible protocol changes.
// 2: harnesses (a session's harness_kind and harness_id; a new session's
// harness_id). Beta: nothing older is kept.
const (
	Proto    = 2
	ProtoMin = 2
)

// Ops.
const (
	OpHello    = "hello"
	OpEvent    = "event"    // a hook event (HookEvent)
	OpPing     = "ping"     // liveness
	OpShutdown = "shutdown" // ask the daemon to exit (version skew)
	OpStatus   = "status"   // counts, for doctor/tests
	OpSnapshot = "snapshot" // sessions, groups and view state (Snapshot)
	OpMutate   = "mutate"   // one change (Mutation)
	OpWatch    = "watch"    // wait for a change (WatchRequest → WatchResponse); a view's heartbeat
	OpNew      = "new"      // start a background session (NewRequest → *core.Session)
	OpResume   = "resume"   // resume a paused background session (Mutation, one target → *core.Session)
	// OpConversations lists earlier Claude and Antigravity conversations to
	// resume (Page → ConversationPage).
	OpConversations = "conversations"
	OpLayout        = "layout"       // the app's pane layout (→ Layout)
	OpSetLayout     = "layout.set"   // stores it (Layout)
	OpInbox         = "inbox"        // inbox items, newest first (InboxRequest → InboxPage)
	OpInboxAnswer   = "inbox.answer" // types an answer into the item's session (InboxAnswer)
	// Scheduled jobs. The snapshot lists them; these change them.
	OpJobSave        = "job.save"         // adds or edits a job (core.Job: ID "" adds) → *core.Job
	OpJobCheck       = "job.check"        // checks a job without saving it (JobCheckRequest) → JobCheck
	OpJobPromptRead  = "job.prompt.read"  // a job's own prompt file (PromptFileRef) → PromptFile
	OpJobPromptWrite = "job.prompt.write" // writes a job's own prompt file (PromptFileRef with Text) → PromptFile
	// This machine's folders, as the Remote API serves a host's: every client
	// chooses folders and prompt files with them.
	OpFoldersRecent = "folders.recent" // no arguments → api.FolderList
	OpFoldersList   = "folders.list"   // api.FolderPath → api.FolderList
	OpFoldersMkdir  = "folders.mkdir"  // api.FolderPath → api.Folder
	OpJobDelete     = "job.delete"     // JobRef.ID
	OpJobPause      = "job.pause"      // JobRef.ID, Paused
	OpJobRun        = "job.run"        // runs it now (JobRef.ID) → core.JobRun
	OpJobRuns       = "job.runs"       // its runs, newest first (JobRunsRequest) → JobRunPage
	OpWakeSet       = "wake.set"       // wakes the Mac for every job or not (WakeRef), checking the sudoers rule again → WakeState
	// Remote sessions: Hosting, this machine's listener for other acmds.
	OpRemoteStatus     = "remote.status"              // → RemoteStatus
	OpRemoteHosting    = "remote.hosting"             // turns Hosting on or off (RemoteRef.Enabled) → RemoteStatus
	OpRemoteReload     = "remote.reload"              // reads config.toml again and applies [remote] → RemoteStatus
	OpRemoteClients    = "remote.clients"             // the authorized clients, with their connections → []RemoteClient
	OpRemoteRequests   = "remote.requests"            // pending requests from unknown clients → []RemoteRequest
	OpRemoteApprove    = "remote.approve"             // authorizes a request's client (RemoteRef.ID, Name) → RemoteClient
	OpRemoteDeny       = "remote.deny"                // drops a request (RemoteRef.ID)
	OpRemoteAuthorize  = "remote.authorize"           // authorizes a client (RemoteRef.Fingerprint, Name) → RemoteClient
	OpRemoteRevoke     = "remote.revoke"              // un-authorizes a client and drops it (RemoteRef.Client) → RemoteClient
	OpRemoteDisconnect = "remote.disconnect"          // drops a client's connections (RemoteRef.Client) → RemoteDisconnected
	OpRemoteRename     = "remote.rename"              // renames an authorized client (RemoteRef.Client, Name) → RemoteClient
	OpRemoteScope      = "remote.scope"               // changes what an authorized client may do (RemoteRef.Client, Scope) → RemoteClient
	OpRemoteIdentity   = "remote.identity"            // this machine's identity, for both roles → RemoteIdentity
	OpRemoteRegenerate = "remote.identity.regenerate" // makes a new identity, and uses it → RemoteIdentity
	OpRemoteCert       = "remote.cert"                // a cert and key of your own for a role, or acmd's again (RemoteCert) → RemoteIdentity
)

// RemoteRef names what a remote.* op acts on. Client is an authorized
// client's name or fingerprint, or for remote.disconnect also a connection's
// id.
type RemoteRef struct {
	ID          string `json:"id,omitempty"`
	Client      string `json:"client,omitempty"`
	Name        string `json:"name,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// Cert is a client's cert file, in place of Fingerprint, to authorize it
	// by (named after the cert when Name is "").
	Cert    string `json:"cert,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`
	// Scope is a client's new scope (OpRemoteScope): read, act or admin.
	Scope string `json:"scope,omitempty"`
}

// RemoteStatus is Hosting: whether it's on, where it listens, and why not
// when it can't.
type RemoteStatus struct {
	Enabled   bool   `json:"enabled"`
	Listening bool   `json:"listening"`
	Listen    string `json:"listen"` // "localhost" or "all"
	// Address and Port are where it actually listens: Port differs from
	// ConfiguredPort when that one was taken and PortFallback picked another.
	Address        string    `json:"address,omitempty"`
	Port           int       `json:"port,omitempty"`
	ConfiguredPort int       `json:"configured_port"`
	PortFallback   bool      `json:"port_fallback"`
	Since          time.Time `json:"since,omitzero"`
	Error          string    `json:"error,omitempty"` // why it isn't listening
	// The identity it shows clients.
	Fingerprint string `json:"fingerprint,omitempty"`
	Name        string `json:"name,omitempty"`
	Cert        string `json:"cert,omitempty"`
	Proto       int    `json:"proto"` // the Remote protocol it speaks
	Authorized  int    `json:"authorized"`
	Connected   int    `json:"connected"` // connections
	Pending     int    `json:"pending"`   // requests waiting for approval
	// Attached is how many client connections are attached to each session,
	// by its id. It changes as they attach, so it reaches clients without
	// their asking.
	Attached map[string]int `json:"attached,omitempty"`
}

// RemoteIdentity is the identity this machine shows: as a host, and, in
// Client, as a client when that's another one.
type RemoteIdentity struct {
	Fingerprint string          `json:"fingerprint"`
	CommonName  string          `json:"common_name"`
	Cert        string          `json:"cert"`
	Key         string          `json:"key"`
	UsedFor     []string        `json:"used_for"` // "hosting", "client"
	Client      *RemoteIdentity `json:"client,omitempty"`
	// Address is where Hosting listens now ("" when it doesn't).
	Address string `json:"address,omitempty"`
}

// RemoteCert names a cert and key of your own for a role ("host" or
// "client"), or, with Default, goes back to acmd's identity. The pair is
// checked before it's saved.
type RemoteCert struct {
	Role    string `json:"role"`
	Cert    string `json:"cert,omitempty"`
	Key     string `json:"key,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// RemoteClient is an authorized client, and its connections.
type RemoteClient struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	// InConfig is a client config.toml lists: it's changed by editing the
	// file, not by ops.
	InConfig    bool               `json:"in_config,omitempty"`
	Connections []RemoteConnection `json:"connections,omitempty"`
	// Scope is what it may do here: read, act or admin.
	Scope string `json:"scope,omitempty"`
}

// RemoteConnection is a client connected now.
type RemoteConnection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"` // its authorized name
	Fingerprint string    `json:"fingerprint"`
	CommonName  string    `json:"common_name,omitempty"` // its cert's
	Addr        string    `json:"addr"`
	Version     string    `json:"version,omitempty"` // its acmd
	Since       time.Time `json:"since,omitzero"`
	Sessions    []string  `json:"sessions,omitempty"` // the sessions it's attached to
}

// RemoteRequest is an unknown client that tried to connect: it gets in once
// it's approved. Repeats from one fingerprint add to Count.
type RemoteRequest struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	CommonName  string    `json:"common_name,omitempty"`
	Addr        string    `json:"addr"`
	FirstSeen   time.Time `json:"first_seen,omitzero"`
	LastSeen    time.Time `json:"last_seen,omitzero"`
	Count       int       `json:"count"`
}

// RemoteDisconnected is how many connections remote.disconnect closed.
type RemoteDisconnected struct {
	Closed int `json:"closed"`
}

// JobRef names a job, and what to do with it.
type JobRef struct {
	ID     string `json:"id,omitempty"`
	Paused bool   `json:"paused,omitempty"`
}

// WakeRef turns waking the Mac for every job on or off.
type WakeRef struct {
	All bool `json:"all"`
}

// WakeState is waking the Mac for jobs: whether acmd may (its sudoers rule is
// in place), whether every job wakes it, and the wake it set.
type WakeState struct {
	Allowed bool      `json:"allowed"`
	All     bool      `json:"all"`
	Next    time.Time `json:"next,omitzero"`
	Error   string    `json:"error,omitempty"`
}

// JobCheck is what the daemon makes of a job before it's saved: its schedule
// read back both ways, the next runs, and what's wrong by field ("name",
// "schedule", "prompt", "where", "cwd", "session", "settings", "group",
// "harness").
type JobCheck struct {
	// Form is the schedule as the form's controls show it (schedule.Form).
	Form  *schedule.Form `json:"form,omitempty"`
	Cron  string         `json:"cron,omitempty"`
	Words string         `json:"words,omitempty"`
	// Summary is the schedule in English, also for cron with no Words.
	Summary string            `json:"summary,omitempty"`
	Next    []time.Time       `json:"next,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// Snapshot is everything a client needs to render the list; clients
// compute the layout themselves with core.Layout.
type Snapshot struct {
	Sessions []*Session `json:"sessions"`
	Groups   []*Group   `json:"groups"`
	// Sets are the sets, oldest first. Their sessions are in Sessions.
	Sets []Set `json:"sets,omitempty"`
	// Others are entities of types with no list of their own (design 7.2):
	// a type added later (a workspace, a folder) goes here, so a client that
	// doesn't know it still sees its type, id, host, parent and order, and
	// draws its children under their nearest ancestor of a type it knows.
	// Each is a JSON object with those common fields and its own.
	Others    []json.RawMessage `json:"others,omitempty"`
	ViewState ViewState         `json:"view_state"`
	// Notices are the most recent notifications, oldest first.
	Notices []Notice `json:"notices,omitempty"`
	// Inbox is the queue: the items still to deal with, in arrival order.
	Inbox []core.InboxItem `json:"inbox,omitempty"`
	// Jobs are the scheduled jobs, by name, with their next and last runs.
	Jobs []*core.Job `json:"jobs,omitempty"`
	// Wake is waking the Mac for jobs (nil: this daemon can't).
	Wake *WakeState `json:"wake,omitempty"`
	// Remote is Hosting (nil: this daemon has none).
	Remote *RemoteStatus `json:"remote,omitempty"`
	// RemoteHosts are the known hosts this machine uses Remote sessions on,
	// with their connections' state. Sessions added from them are in
	// Sessions, with Host set.
	RemoteHosts []RemoteHost `json:"remote_hosts,omitempty"`
	// Harnesses are this machine's harnesses, in list order.
	Harnesses []Harness `json:"harnesses,omitempty"`
	// Settings are the settings in effect, by key (ConfigState.Values).
	// config.get says what each one is.
	Settings map[string]any `json:"settings,omitempty"`
	// Health is what's wrong with the host, or worth knowing.
	Health []Health `json:"health,omitempty"`
	// Dismissed are the health ids the asking client dismissed
	// (SnapshotRequest.ClientID).
	Dismissed []string `json:"dismissed,omitempty"`
	// Features say what the host and this device can do.
	Features *Features `json:"features,omitempty"`
	// Update is this daemon's version, and a newer release if there is one.
	Update *Update `json:"update,omitempty"`
	// Speech is how reading aloud stands on this machine.
	Speech *SpeechState `json:"speech,omitempty"`
}

// Notice is a notification the daemon decided on, and the clients it chose
// to show it (To).
type Notice struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"` // waiting | error | done | paused | withdraw
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Urgent    bool   `json:"urgent,omitempty"`
	Sound     string `json:"sound,omitempty"`
	Host      string `json:"host,omitempty"` // the Remote host, for a session added from one
	// To are the clients that show it, by id (notify.route). None: the host
	// showed it itself.
	To []string `json:"to,omitempty"`
}

// Mutation kinds (OpMutate). Selectors are resolved by the client; the
// daemon gets ids.
const (
	MutRename         = "session.rename"    // ID, Text
	MutSeen           = "session.seen"      // ID: you've looked at it
	MutDescribe       = "session.describe"  // ID, Text
	MutArchive        = "session.archive"   // ID
	MutFinish         = "session.finish"    // ID: stop it (claude stop) and mark it finished
	MutUnarchive      = "session.unarchive" // ID
	MutMove           = "session.move"      // IDs, GroupID
	MutGroupCreate    = "group.create"      // Text (name), Color -> returns the group
	MutGroupRename    = "group.rename"      // GroupID, Text
	MutGroupColor     = "group.color"       // GroupID, Color
	MutGroupDelete    = "group.delete"      // GroupID
	MutGroupArchive   = "group.archive"     // GroupID (archives its sessions too)
	MutGroupUnarchive = "group.unarchive"   // GroupID, Flag = also unarchive sessions
	MutGroupCollapse  = "group.collapse"    // GroupID ("" = all), Flag = collapsed
	MutGroupMove      = "group.move"        // GroupID, After or Before: the group it goes next to
	MutViewMode       = "view.mode"         // Text: groups|list
	MutViewSort       = "view.sort"         // Text: key, Flag = descending
	MutViewFocus      = "view.focus"        // GroupID ("" = leave focus mode)
	MutViewFilter     = "view.filter"       // Text
	MutViewArchived   = "view.archived"     // Flag = show archived
	MutInboxRead      = "inbox.read"        // ItemIDs, Flag = read (else unread)
	MutInboxDismiss   = "inbox.dismiss"     // ItemIDs: take them out of the queue
	MutPurge          = "session.purge"     // IDs: archived sessions to delete for good (claude rm) -> returns the count
	MutRemote         = "session.remote"    // ID, Flag = Remote: clients of Hosting may see and use it
	// Sets: sessions made together by splitting panes (section 7 of the
	// design). A session is in one set at most; a set has at least two
	// sessions, all in one group and on one host, and ends when one is left.
	MutSetCreate = "set.create" // IDs (in order), SetID ("" = a new id) -> returns the Set
	MutSetAdd    = "set.add"    // SetID, ID, After (the member it goes after; "" = the end)
	MutSetRemove = "set.remove" // ID: out of its set; it keeps running on its own
	MutSetEnd    = "set.end"    // SetID: its sessions stay, on their own
)

// EntitySet is a Set's type.
const EntitySet = "set"

// Set is a set as clients see it. Its sessions name it as their parent, with
// their place in it as their order.
type Set struct {
	Type   string `json:"type"`   // EntitySet
	ID     string `json:"id"`     //
	Parent string `json:"parent"` // its group
	Host   string `json:"host,omitempty"`
}

// Mutation is one change requested by a client.
type Mutation struct {
	Kind string `json:"kind"`
	// Targets are the sessions it changes, each on its host ("" for this
	// machine): design 7.4's {host?, id}. A kind that refers to a group, set
	// or neighbor (move, the set kinds) changes only sessions on Host; the
	// others may span hosts, and this machine's daemon splits them per host,
	// with a Result for each target.
	Targets []Target `json:"targets,omitempty"`
	// Items are the Inbox items it changes (inbox.read, inbox.dismiss), each
	// with its host's own id.
	Items []ItemTarget `json:"items,omitempty"`
	// LocalID, LocalIDs and LocalItemIDs are what a daemon runs on its own
	// machine, set from Targets and Items once it has split the mutation by
	// host (one target is also LocalID): never sent. Clients set Targets.
	LocalID      string   `json:"-"`
	LocalIDs     []string `json:"-"`
	LocalItemIDs []int64  `json:"-"`
	GroupID      string   `json:"group_id,omitempty"`
	Text         string   `json:"text,omitempty"`
	Color        string   `json:"color,omitempty"`
	Flag         bool     `json:"flag,omitempty"`
	Pane         *PaneLoc `json:"pane,omitempty"`
	SetID        string   `json:"set_id,omitempty"`
	After        string   `json:"after,omitempty"`
	Before       string   `json:"before,omitempty"`
	// Host is the machine its group, set and neighbor are on, and the one
	// a mutation with no targets is for (the group kinds, a new group): a
	// known Remote host's name, or "" for this machine. A host is always
	// named, never found from an id: ids are unique only within their host,
	// and every host's Default group is "default".
	Host string `json:"host,omitempty"`
}

// Target names a session, group or set on its host: Host is a known Remote
// host's name, "" for the machine the daemon is on.
type Target struct {
	Host string `json:"host,omitempty"`
	ID   string `json:"id"`
}

// ItemTarget names an Inbox item on its host, by the host's own id.
type ItemTarget struct {
	Host string `json:"host,omitempty"`
	ID   int64  `json:"id"`
}

// Here are targets on this machine, for ids.
func Here(ids ...string) []Target {
	out := make([]Target, len(ids))
	for i, id := range ids {
		out[i] = Target{ID: id}
	}
	return out
}

// On are targets on host, for ids.
func On(host string, ids ...string) []Target {
	out := Here(ids...)
	for i := range out {
		out[i].Host = host
	}
	return out
}

// InboxRequest asks for one session's inbox items, or everyone's with no SessionID.
type InboxRequest struct {
	Host      string `json:"host,omitempty"` // the session's host; "" for this machine
	SessionID string `json:"session_id,omitempty"`
	Page
}

// InboxAnswer answers an item: Option (from 1) for a question or permission, Text
// for anything else.
type InboxAnswer struct {
	// Host is the item's host ("" for this machine), and ID its id there.
	Host   string `json:"host,omitempty"`
	ID     int64  `json:"id"`
	Option int    `json:"option,omitempty"`
	Text   string `json:"text,omitempty"`
}

// WatchRequest waits until the state revision differs from Rev (or Wait
// passes).
type WatchRequest struct {
	Rev    uint64 `json:"rev"`
	WaitMS int    `json:"wait_ms,omitempty"`
}

// WatchResponse is the current revision.
// when it was closed or pruned).
type WatchResponse struct {
	Rev uint64 `json:"rev"`
}

// NewRequest starts a background session. The client resolves
// the group and folder; ShowIn is the view that switches to it ("" = none).
type NewRequest struct {
	GroupID string `json:"group_id,omitempty"`
	Cwd     string `json:"cwd"`
	// HarnessID is the harness it runs in; with none, HarnessKind
	// "terminal" is a terminal, and another kind (or none) its default
	// harness (Harnesses.Default for none).
	HarnessID   string `json:"harness_id,omitempty"`
	HarnessKind string `json:"harness_kind,omitempty"`
	// Resume continues this conversation, by its id or an id prefix, in its
	// harness (Conversation.HarnessID). Its folder comes from the
	// conversation, so Cwd can be empty.
	Resume      string   `json:"resume,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	ShowIn      string   `json:"show_in,omitempty"`
	// Remote makes it a Remote session, or not. nil (as from ⌘N) is the
	// config's [remote] new_sessions_remote.
	Remote *bool `json:"remote,omitempty"`
	// SetWith is a session to put it in a set with: that session's set, right
	// after it, or a new set of the two. It goes in that session's group.
	SetWith string `json:"set_with,omitempty"`
	// JobID is the scheduled job starting it. The daemon sets it for its own
	// runs; it isn't taken from clients.
	JobID string `json:"-"`
}

// Layout is the app's pane layout. It belongs to the app: the daemon only keeps
// it, so it comes back after a restart.
type Layout struct {
	// ClientID is the client whose layout it is; each keeps its own. ""
	// is the one layout from before clients had ids.
	ClientID string `json:"client_id,omitempty"`
	JSON     string `json:"json"`
}

// Conversation is an earlier Claude or Antigravity conversation that can be
// resumed.
type Conversation struct {
	ID string `json:"id"`
	// HarnessID is the harness whose history it's in, and HarnessKind its
	// kind. NewRequest takes them with the id to resume it.
	HarnessID   string    `json:"harness_id,omitempty"`
	HarnessKind string    `json:"harness_kind"`
	Cwd         string    `json:"cwd"`
	Title       string    `json:"title,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
	// Running: Claude reports it as live, possibly in another terminal (agy
	// reports none).
	Running bool `json:"running,omitempty"`
	// SessionID is the hosted acmd session that already has it ("" = none).
	SessionID string `json:"session_id,omitempty"`
}

// Request is one client message.
type Request struct {
	Op   string          `json:"op"`
	Data json.RawMessage `json:"data,omitempty"`
	// RequestID makes a creating op safe to retry (the catalog marks which
	// are): the same id within RequestKeep gets the first result back,
	// instead of a second session, job, run or group. Any unique string,
	// such as a UUID.
	RequestID string `json:"request_id,omitempty"`
}

// RequestKeep is how long the daemon keeps a request id's result.
const RequestKeep = 10 * time.Minute

type requestIDKey struct{}

// WithRequestID carries a request id with ctx, on its way to the daemon's
// ops or a host's.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

type clientKey struct{}

// WithClient carries the UI an op is for with ctx, on its way to a host.
func WithClient(ctx context.Context, c *ClientHello) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, clientKey{}, c)
}

// ClientOf is the UI ctx carries, or nil.
func ClientOf(ctx context.Context) *ClientHello {
	c, _ := ctx.Value(clientKey{}).(*ClientHello)
	return c
}

// RequestIDOf is the request id ctx carries, or "".
func RequestIDOf(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Response answers a Request.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Code says what kind of error, when the daemon knows (CodeNotSupported,
	// CodeNotRunning, CodeNotFound, CodeInvalid…).
	Code string          `json:"code,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Hello is exchanged first in both directions.
type Hello struct {
	// ProtoMin and ProtoMax are the versions a side speaks. In the daemon's
	// reply, Proto is the one chosen; a client's hello with only Proto speaks
	// just that one.
	Proto    int    `json:"proto"`
	ProtoMin int    `json:"proto_min,omitempty"`
	ProtoMax int    `json:"proto_max,omitempty"`
	Version  string `json:"version"`
	Pid      int    `json:"pid,omitempty"`
	// Ops are the daemon's ops, in its reply: what a client can use.
	Ops []string `json:"ops,omitempty"`
	// The client, in a client's hello: who it is, so the daemon knows who's
	// there (OpClients) and who shows each notice. A client without an id
	// (the CLI, a hook) isn't registered.
	Client *ClientHello `json:"client,omitempty"`
}

// ClientHello is what a client says about itself on every connection.
type ClientHello struct {
	// ID is the client's own, kept by it across runs.
	ID     string `json:"id"`
	Kind   string `json:"kind"`             // a Kind*: mac, linux, windows, terminal, android, cli
	Device string `json:"device,omitempty"` // the machine's name, for people
	// Caps are what it can do: Cap* values.
	Caps []string `json:"caps,omitempty"`
	// OnHost: it runs on the host's own machine.
	OnHost bool `json:"on_host,omitempty"`
}

// Client kinds. More may come: a client shows one it doesn't know as it is.
const (
	KindMac      = "mac"
	KindLinux    = "linux"
	KindWindows  = "windows"
	KindTerminal = "terminal" // acmd attach, acmd watch and the terminal UI
	KindAndroid  = "android"
	KindCLI      = "cli"
	// KindMachine is a paired machine, as its host sees it: its daemon stands
	// for its UIs, and passes a notice on to the one you're using there.
	KindMachine = "machine"
)

// What a client can do.
const (
	CapNotify = "notify" // shows notices
	CapSound  = "sound"  // plays their sound
	CapAttach = "attach" // shows a session's terminal
	CapSpeech = "speech" // reads aloud
)

// Presence is how the person is using a client.
const (
	PresenceActive       = "active"       // in front, in use
	PresenceIdle         = "idle"         // open, but no input for a few minutes
	PresenceBackground   = "background"   // open, but not in front
	PresenceDisconnected = "disconnected" // not connected (the daemon says, not the client)
)

// OpClients lists the client registry (→ []Client). OpClientPresence is a
// client saying how it's used (ClientPresence; no data). OpNoticeGet reads
// a recent notice by its id (NoticeRef → Notice).
const (
	OpClients        = "clients"
	OpClientPresence = "client.presence"
	OpNoticeGet      = "notice.get"
)

// Client is one client in the registry.
type Client struct {
	ClientHello
	// Presence is PresenceDisconnected when it isn't connected.
	Presence string `json:"presence"`
	// Focus is the session it shows, when it says.
	Focus      string    `json:"focus,omitempty"`
	Connected  bool      `json:"connected"`
	FirstSeen  time.Time `json:"first_seen,omitzero"`
	LastSeen   time.Time `json:"last_seen,omitzero"`
	LastActive time.Time `json:"last_active,omitzero"`
}

// ClientPresence is a client saying how it's used now, and the session it
// shows ("" for none).
type ClientPresence struct {
	Presence string `json:"presence"`
	Focus    string `json:"focus,omitempty"`
}

// The calling client's own state in the daemon. OpClientProfile reads its
// notification profile (→ ClientProfile), OpClientProfileSet changes it
// (ClientProfile → ClientProfile). OpKeysSet registers its keymap (Keymap's
// map: no data), and OpKeys reads one (KeysRequest → Keymap): acmd keys.
const (
	OpClientProfile    = "client.profile"
	OpClientProfileSet = "client.profile.set"
	OpKeysSet          = "keys.set"
	OpKeys             = "keys"
)

// ClientProfile is how a client notifies.
type ClientProfile struct {
	// Notify are the notice kinds it shows (waiting, error, done, paused);
	// none means every kind.
	Notify []string `json:"notify,omitempty"`
	// PopupOpens is what opens its popup: PopupNeedsYou, PopupEverything or
	// PopupNever ("" for the client's own default).
	PopupOpens string `json:"popup_opens,omitempty"`
}

// What opens a client's popup (ClientProfile.PopupOpens).
const (
	PopupNeedsYou   = "needs_you"
	PopupEverything = "everything"
	PopupNever      = "never"
)

// KeysRequest names whose keymap to read: a client's id, or "" for the
// desktop client seen most recently.
type KeysRequest struct {
	ClientID string `json:"client_id,omitempty"`
}

// Keymap is a client's keyboard shortcuts, as it registered them: its own
// shape (for the macOS app, its sections of actions and keys).
type Keymap struct {
	ClientID string          `json:"client_id"`
	Kind     string          `json:"kind,omitempty"`
	Device   string          `json:"device,omitempty"`
	Map      json.RawMessage `json:"map"`
}

// NoticeRef names a notice by its id.
type NoticeRef struct {
	ID uint64 `json:"id"`
}

// HookEvent is what `acmd hook` sends: the raw payload is parsed by the
// daemon (one parser, core.ParseHook) plus the context only the hook
// process can see.
type HookEvent struct {
	Name    string          `json:"name"`
	At      time.Time       `json:"at,omitzero"`
	Payload json.RawMessage `json:"payload"`
	Context HookContext     `json:"context"`
}

// HookContext is process context gathered by `acmd hook`.
type HookContext struct {
	ClaudePid   int    `json:"claude_pid,omitempty"`
	HostSession string `json:"host_session,omitempty"` // ACMD_SESSION_ID: the acmd session it runs in
}

// Status is the daemon's summary.
type Status struct {
	Version  string `json:"version"`
	Pid      int    `json:"pid"`
	Sessions int    `json:"sessions"`
	Events   int    `json:"events"`
	// RemoteSessions counts the Remote sessions that aren't archived.
	RemoteSessions int `json:"remote_sessions,omitempty"`
}

// RemoteError is an error the daemon reported (the connection is fine).
type RemoteError struct{ Msg, Code string }

func (e *RemoteError) Error() string { return e.Msg }

// ErrVersionMismatch is returned by Dial when the daemon runs another version.
type ErrVersionMismatch struct{ Daemon Hello }

func (e *ErrVersionMismatch) Error() string {
	return fmt.Sprintf("daemon is version %s (proto %d)", e.Daemon.Version, e.Daemon.Proto)
}

// Conn is a client connection after a successful hello.
type Conn struct {
	c      net.Conn
	r      *bufio.Reader
	Daemon Hello
}

// MaxLine bounds a single protocol message.
const MaxLine = 2 << 20

// Dial connects and exchanges hellos. On a version or proto mismatch it
// returns the open connection together with *ErrVersionMismatch, so the
// caller can ask that daemon to shut down.
func Dial(ctx context.Context, socket string, me Hello) (*Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	conn := &Conn{c: c, r: bufio.NewReaderSize(c, 64*1024)}
	var theirs Hello
	if err := conn.Call(OpHello, me, &theirs); err != nil {
		_ = c.Close()
		return nil, err
	}
	conn.Daemon = theirs
	// The protocol was agreed in the hello (a daemon with none in common
	// refuses it). Another build is still a mismatch: the CLI replaces a
	// daemon left running from before an upgrade.
	if theirs.Version != me.Version {
		return conn, &ErrVersionMismatch{Daemon: theirs}
	}
	return conn, nil
}

// HelloFrom is a client's hello: its version, and the protocol versions
// this build speaks.
func HelloFrom(version string) Hello {
	return Hello{Proto: Proto, ProtoMin: ProtoMin, ProtoMax: Proto, Version: version}
}

// Has says whether the daemon has op: a client shows what it offers.
func (c *Conn) Has(op string) bool { return slices.Contains(c.Daemon.Ops, op) }

// Call sends one request and decodes the response data into out (if non-nil).
func (c *Conn) Call(op string, in, out any) error { return c.CallOnce(op, "", in, out) }

// CallOnce is Call with a request id, for a creating op that's safe to retry
// with the same id: it runs once.
func (c *Conn) CallOnce(op, requestID string, in, out any) error {
	req := Request{Op: op, RequestID: requestID}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		req.Data = b
	}
	if err := WriteJSON(c.c, req); err != nil {
		return err
	}
	var resp Response
	if err := ReadJSON(c.r, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return &RemoteError{Msg: resp.Error, Code: resp.Code}
	}
	if out != nil && len(resp.Data) > 0 {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

// Close closes the connection.
func (c *Conn) Close() error { return c.c.Close() }

// WriteJSON writes v as one line.
func WriteJSON(w net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ReadJSON reads one line into v.
func ReadJSON(r *bufio.Reader, v any) error {
	line, err := readLine(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > MaxLine {
			return nil, errors.New("message too large")
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

// Many ids. rename (Text: the name, numbered "api - 1", "api - 2" for
// several), archive, unarchive, remote (Flag), remove_added, move (GroupID)
// and purge take IDs, of any session, and return Results: one for each id, in
// order. Every id on this machine that can change does, and those that can't
// say why; ids on Remote hosts are tried one by one, and once a host can't
// be reached, its other ids say so without waiting for it again.
type Results struct {
	Results []Result `json:"results"`
}

// Result is what happened to one id: done, or an error with a code.
type Result struct {
	Host  string `json:"host,omitempty"` // the target's host; "" for this machine
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"` // a Code*, or a host's own
	Error string `json:"error,omitempty"`
}

// Error codes a Result or a Response carries: errcode's, which every
// transport answers with, and the catalog lists.
const (
	CodeNotFound        = errcode.NotFound
	CodeInvalid         = errcode.Invalid
	CodeBadRequest      = errcode.BadRequest
	CodeConflict        = errcode.Conflict
	CodePermission      = errcode.PermissionDenied
	CodeHostUnreachable = errcode.HostUnreachable
	CodeUnknownOp       = errcode.UnknownOp
	CodeNotSupported    = errcode.NotSupported
	CodeNotRunning      = errcode.NotRunning
	CodeInternal        = errcode.Internal
)

// Failed says how many results aren't OK.
func (r Results) Failed() int {
	n := 0
	for _, x := range r.Results {
		if !x.OK {
			n++
		}
	}
	return n
}

// JobCheckRequest is a job to check or save. With Form and no Schedule, its
// schedule is the form's, so a client maps no cron itself; a Schedule as
// typed wins.
type JobCheckRequest struct {
	core.Job
	Form *schedule.Form `json:"form,omitempty"`
}

// PromptFileRef names a prompt file: a saved job's own, by JobID, or a
// draft's, by its folder and path (a Markdown or text file only). Text is
// what to write.
type PromptFileRef struct {
	JobID string `json:"job_id,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	Path  string `json:"path,omitempty"`
	Text  string `json:"text,omitempty"`
}

// PromptFile is a prompt file's full path and text.
type PromptFile struct {
	Path   string `json:"path"`
	Text   string `json:"text"`
	Exists bool   `json:"exists"`
}

// OpSessionScreen reads a session's screen and recent history as plain text
// (ScreenRequest → Screen). Sessions on this machine only, for now: one on a
// Remote host fails with CodeNotSupported.
const OpSessionScreen = "session.screen"

// OpSessionAttach attaches to a session's terminal, as a stream of its frames
// (internal/term). On this machine a client connects to the terminal's own
// socket; a paired machine uses the Remote API's session.attach, checked as
// this op.
const OpSessionAttach = "session.attach"

// ScreenRequest names the session, and how much to send: Lines is the last
// lines of the screen and history (0: all of it).
type ScreenRequest struct {
	Host  string `json:"host,omitempty"` // the session's host; "" for this machine
	ID    string `json:"id"`
	Lines int    `json:"lines,omitempty"`
}

// Screen is a session's screen as text.
type Screen struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Lines is how many lines Text has, and Total how many there were before
	// Lines cut it.
	Lines int `json:"lines"`
	Total int `json:"total"`
}

// HereItems are Inbox item targets on this machine, for ids.
func HereItems(ids ...int64) []ItemTarget {
	out := make([]ItemTarget, len(ids))
	for i, id := range ids {
		out[i] = ItemTarget{ID: id}
	}
	return out
}
