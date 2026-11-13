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
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

// Proto is bumped only on incompatible protocol changes.
const Proto = 1

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
	OpResume   = "resume"   // resume a paused background session (Mutation.ID → *core.Session)
	// OpConversations lists earlier Claude sessions to resume (→ []Conversation).
	OpConversations = "conversations"
	OpLayout        = "layout"       // the app's pane layout (→ Layout)
	OpSetLayout     = "layout.set"   // stores it (Layout)
	OpInbox         = "inbox"        // inbox items, newest first (InboxRequest → []core.InboxItem)
	OpInboxAnswer   = "inbox.answer" // types an answer into the item's session (InboxAnswer)
	// Scheduled jobs. The snapshot lists them; these change them.
	OpJobSave       = "job.save"       // adds or edits a job (core.Job: ID "" adds) → *core.Job
	OpJobCheck      = "job.check"      // checks a job without saving it (core.Job) → JobCheck
	OpJobDelete     = "job.delete"     // JobRef.ID
	OpJobPause      = "job.pause"      // JobRef.ID, Paused
	OpJobRun        = "job.run"        // runs it now (JobRef.ID) → core.JobRun
	OpJobRuns       = "job.runs"       // its runs, newest first (JobRef.ID) → []core.JobRun
	OpAgentSettings = "agent.settings" // an agent's settings for jobs (JobRef.Harness) → []harness.Setting
	OpWakeSet       = "wake.set"       // wakes the Mac for every job or not (WakeRef), checking the sudoers rule again → WakeState
)

// JobRef names a job, and what to do with it.
type JobRef struct {
	ID      string `json:"id,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
	Harness string `json:"harness,omitempty"`
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
	Cron  string `json:"cron,omitempty"`
	Words string `json:"words,omitempty"`
	// Summary is the schedule in English, also for cron with no Words.
	Summary string            `json:"summary,omitempty"`
	Next    []time.Time       `json:"next,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// Snapshot is everything a client needs to render the list; clients
// compute the layout themselves with core.Layout.
type Snapshot struct {
	Sessions  []*core.Session `json:"sessions"`
	Groups    []*core.Group   `json:"groups"`
	ViewState core.ViewState  `json:"view_state"`
	// Notices are the most recent notifications, oldest first.
	Notices []Notice `json:"notices,omitempty"`
	// Inbox is the queue: the items still to deal with, in arrival order.
	Inbox []core.InboxItem `json:"inbox,omitempty"`
	// Jobs are the scheduled jobs, by name, with their next and last runs.
	Jobs []*core.Job `json:"jobs,omitempty"`
	// Wake is waking the Mac for jobs (nil: this daemon can't).
	Wake *WakeState `json:"wake,omitempty"`
}

// Notice is a notification the daemon decided on. Emitter is the view
// whose sidebar shows it; "" means the daemon showed it itself (no view
// with a visible sidebar).
type Notice struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"` // waiting | error | done | paused | withdraw
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Urgent    bool   `json:"urgent,omitempty"`
	Sound     string `json:"sound,omitempty"`
	Emitter   string `json:"emitter,omitempty"` // a view's id, or EmitterApp
}

// EmitterApp in a Notice means the app shows it.
const EmitterApp = "app"

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
	MutGroupMove      = "group.move"        // GroupID, Flag = down (else up)
	MutViewMode       = "view.mode"         // Text: groups|list
	MutViewSort       = "view.sort"         // Text: key, Flag = descending
	MutViewFocus      = "view.focus"        // GroupID ("" = leave focus mode)
	MutViewFilter     = "view.filter"       // Text
	MutViewArchived   = "view.archived"     // Flag = show archived
	MutInboxRead      = "inbox.read"        // ItemIDs, Flag = read (else unread)
	MutInboxDismiss   = "inbox.dismiss"     // ItemIDs: take them out of the queue
	MutPurge          = "session.purge"     // IDs: archived sessions to delete for good (claude rm) -> returns the count
)

// Mutation is one change requested by a client.
type Mutation struct {
	Kind    string        `json:"kind"`
	ID      string        `json:"id,omitempty"`
	IDs     []string      `json:"ids,omitempty"`
	ItemIDs []int64       `json:"item_ids,omitempty"`
	GroupID string        `json:"group_id,omitempty"`
	Text    string        `json:"text,omitempty"`
	Color   string        `json:"color,omitempty"`
	Flag    bool          `json:"flag,omitempty"`
	Pane    *core.PaneLoc `json:"pane,omitempty"`
}

// InboxRequest asks for one session's inbox items, or everyone's with no SessionID.
type InboxRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

// InboxAnswer answers an item: Option (from 1) for a question or permission, Text
// for anything else.
type InboxAnswer struct {
	ID     int64  `json:"id"`
	Option int    `json:"option,omitempty"`
	Text   string `json:"text,omitempty"`
}

// WatchRequest waits until the state revision differs from Rev (or Wait
// passes).
type WatchRequest struct {
	Rev    uint64 `json:"rev"`
	WaitMS int    `json:"wait_ms,omitempty"`
	// App marks the app's watch, which tells the daemon the app can show notices.
	App bool `json:"app,omitempty"`
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
	Harness string `json:"harness,omitempty"` // "" = claude
	// Resume continues this Claude conversation, by its id or an id prefix.
	// Its folder comes from the conversation, so Cwd can be empty.
	Resume      string   `json:"resume,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	ShowIn      string   `json:"show_in,omitempty"`
}

// Layout is the app's pane layout. It belongs to the app: the daemon only keeps
// it, so it comes back after a restart.
type Layout struct {
	JSON string `json:"json"`
}

// Conversation is an earlier Claude session that can be resumed.
type Conversation struct {
	ID        string    `json:"id"`
	Cwd       string    `json:"cwd"`
	Title     string    `json:"title,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Running: Claude reports it as live, possibly in another terminal.
	Running bool `json:"running,omitempty"`
	// SessionID is the hosted acmd session that already has it ("" = none).
	SessionID string `json:"session_id,omitempty"`
}

// Request is one client message.
type Request struct {
	Op   string          `json:"op"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Response answers a Request.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Hello is exchanged first in both directions.
type Hello struct {
	Proto   int    `json:"proto"`
	Version string `json:"version"`
	Pid     int    `json:"pid,omitempty"`
}

// HookEvent is what `acmd hook` sends: the raw payload is parsed by the
// daemon (one parser, core.ParseHook) plus the context only the hook
// process can see.
type HookEvent struct {
	Name    string          `json:"name"`
	At      time.Time       `json:"at"`
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
}

// RemoteError is an error the daemon reported (the connection is fine).
type RemoteError struct{ Msg string }

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
	if theirs.Proto != me.Proto || theirs.Version != me.Version {
		return conn, &ErrVersionMismatch{Daemon: theirs}
	}
	return conn, nil
}

// Call sends one request and decodes the response data into out (if non-nil).
func (c *Conn) Call(op string, in, out any) error {
	req := Request{Op: op}
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
		return &RemoteError{Msg: resp.Error}
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
