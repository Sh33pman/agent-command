// Package ipc is acmd's local protocol: newline-delimited JSON over the
// daemon's unix socket (SPEC §4.3). Every connection starts with a hello
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
)

// Snapshot is everything a client needs to render the list; clients
// compute the layout themselves with core.Layout.
type Snapshot struct {
	Sessions  []*core.Session `json:"sessions"`
	Groups    []*core.Group   `json:"groups"`
	ViewState core.ViewState  `json:"view_state"`
	Views     []*core.View    `json:"views"`
}

// Mutation kinds (OpMutate). Selectors are resolved by the client; the
// daemon gets ids.
const (
	MutRename         = "session.rename"    // ID, Text
	MutDescribe       = "session.describe"  // ID, Text
	MutArchive        = "session.archive"   // ID
	MutUnarchive      = "session.unarchive" // ID
	MutMove           = "session.move"      // IDs, GroupID
	MutGroupCreate    = "group.create"      // Text (name), Color -> returns the group
	MutGroupRename    = "group.rename"      // GroupID, Text
	MutGroupColor     = "group.color"       // GroupID, Color
	MutGroupDelete    = "group.delete"      // GroupID
	MutGroupArchive   = "group.archive"     // GroupID (archives its sessions too)
	MutGroupUnarchive = "group.unarchive"   // GroupID, Flag = also unarchive sessions
	MutGroupCollapse  = "group.collapse"    // GroupID ("" = all), Flag = collapsed
	MutViewMode       = "view.mode"         // Text: groups|list
	MutViewSort       = "view.sort"         // Text: key, Flag = descending
	MutViewFocus      = "view.focus"        // GroupID ("" = leave focus mode)
	MutViewFilter     = "view.filter"       // Text
	MutViewArchived   = "view.archived"     // Flag = show archived
	MutViewOpen       = "view.open"         // Pane, ID = session to show ("" = most recently active) -> returns the view
	MutViewSelect     = "view.select"       // ID = view, Text = session ("" = placeholder)
	MutViewClose      = "view.close"        // ID = view
	MutViewTouch      = "view.touch"        // ID = view: it was focused
)

// Mutation is one change requested by a client.
type Mutation struct {
	Kind    string        `json:"kind"`
	ID      string        `json:"id,omitempty"`
	IDs     []string      `json:"ids,omitempty"`
	GroupID string        `json:"group_id,omitempty"`
	Text    string        `json:"text,omitempty"`
	Color   string        `json:"color,omitempty"`
	Flag    bool          `json:"flag,omitempty"`
	Pane    *core.PaneLoc `json:"pane,omitempty"`
}

// WatchRequest waits until the state revision differs from Rev (or Wait
// passes). With a ViewID it also counts as that view's heartbeat.
type WatchRequest struct {
	Rev    uint64 `json:"rev"`
	ViewID string `json:"view_id,omitempty"`
	WaitMS int    `json:"wait_ms,omitempty"`
}

// WatchResponse is the current revision and, for a ViewID, the view (Gone
// when it was closed or pruned).
type WatchResponse struct {
	Rev  uint64     `json:"rev"`
	View *core.View `json:"view,omitempty"`
	Gone bool       `json:"gone,omitempty"`
}

// NewRequest starts a background session (SPEC §6.1). The client resolves
// the group and folder; ShowIn is the view that switches to it ("" = none).
type NewRequest struct {
	GroupID     string   `json:"group_id,omitempty"`
	Cwd         string   `json:"cwd"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	ShowIn      string   `json:"show_in,omitempty"`
}

// ErrNotTrustedText marks a NewRequest refused because Claude hasn't
// trusted the folder yet (the client runs the trust step and retries).
const ErrNotTrustedText = "workspace not trusted by Claude"

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

// HookContext is terminal/process context gathered by `acmd hook`.
type HookContext struct {
	ClaudePid     int    `json:"claude_pid,omitempty"`
	KittyListenOn string `json:"kitty_listen_on,omitempty"`
	KittyWindowID string `json:"kitty_window_id,omitempty"`
	TermProgram   string `json:"term_program,omitempty"`
	ViewID        string `json:"view_id,omitempty"` // ACMD_VIEW_ID: started by acmd
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
