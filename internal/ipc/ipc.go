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
)

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
		return errors.New(resp.Error)
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
