// Package term keeps a shell alive per session for the terminal pane
// under a view's Claude pane. A server runs the shell in a
// pseudo-terminal and keeps its recent output; the pane attaches to the
// server of the session the view shows, like `claude attach`, so the
// shell and what runs in it carry on while another session is shown.
//
// Protocol on the server's unix socket: the server writes the shell's
// output as raw bytes (first what restores the current state); the client
// sends frames of [type byte][length uint32 BE][payload]:
// 'a' attach (first frame; a connection without it only probes or ends the
// terminal), 'i' input, 'r' resize (rows, cols as uint16 BE), 'k' end the
// terminal, 'w' type the payload into the shell and close (Send), 't' reply
// with the screen as plain text and close (ScreenText), 'f' reply with the
// program in front as JSON and close (Foreground), 'm' reply whether the
// server has the mode in the payload, as JSON, and close (CheckMode).
//
// Hosts outlive upgrades, so a client can meet a server older or newer than
// itself. Add to this protocol, don't change it: a server ignores frames it
// doesn't know, and a client still works if an attach payload is ignored.
// The attach payload is the protocol version, rows and cols as uint16 BE, then
// the mode (Mode*) as text; any of it may be missing. A server closes an
// attach for a mode it doesn't have.
//
// # Modes
//
// An attach asks for a mode (design D8). ModeStream, the only one now, is
// the terminal's own bytes, for a client with an emulator; no mode is
// ModeStream, so clients and servers from before modes work unchanged. A
// client asking for any other mode checks it first (CheckMode), and falls
// back when the server says not_supported, or is older than the question.
//
// Planned, not built: ModeRendered, for thin clients with no emulator
// (Android, a first Windows client). The host's libghostty-vt has the exact
// screen, so it sends it as cells: the screen's size, each cell's text,
// colors and attributes, and the cursor, then only the cells that change,
// a frame at a time. Input and resize are the same frames as for a stream.
package term

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/brettmostert/agent-command/internal/errcode"
)

// Frame types (client to server).
const (
	frameAttach = 'a' // first frame of a client that wants the output
	frameInput  = 'i'
	frameResize = 'r'
	frameKill   = 'k'
	frameWrite  = 'w' // input from a connection that doesn't attach (Send)
	frameText   = 't' // the screen as plain text (ScreenText)
	frameFront  = 'f' // the program in front (Foreground)
	frameAgent  = 'g' // the agent's last OSC 7501 report (AgentStatus)
	frameMode   = 'm' // whether the server has a mode (CheckMode)
)

// Attach modes.
const (
	// ModeStream is the terminal's own output, as bytes: what a client with
	// a terminal emulator draws.
	ModeStream = "stream"
	// ModeRendered is planned (see the package doc): the screen as cells,
	// then its changes. No server has it yet.
	ModeRendered = "rendered"
)

// modes are the modes this server attaches with.
var modes = []string{ModeStream}

// HasMode says whether this build's servers attach with mode ("" is
// ModeStream).
func HasMode(mode string) bool { return mode == "" || slices.Contains(modes, mode) }

// ModeError is an attach mode a server doesn't have: a client falls back to
// ModeStream.
type ModeError struct{ Mode string }

func (e *ModeError) Error() string {
	return fmt.Sprintf("the terminal can't attach in %q mode", e.Mode)
}

// Code is its error code.
func (e *ModeError) Code() string { return errcode.NotSupported }

// Front is the program in front of a terminal: the one the keyboard reaches.
type Front struct {
	Shell bool   `json:"shell,omitempty"` // the terminal's own shell
	Name  string `json:"name,omitempty"`  // otherwise, the program's name
}

// protocolVersion goes in the attach payload.
const protocolVersion = 1

// Keep is how much recent output a server without libghostty-vt replays to a
// client that attaches (256 KiB).
const Keep = 256 << 10

// ErrEnded means the terminal's shell exited (or it was ended).
var ErrEnded = errors.New("terminal ended")

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	hdr := make([]byte, 5, 5+len(payload))
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload))) // #nosec G115 -- frames are small
	_, err := w.Write(append(hdr, payload...))
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > 1<<20 {
		return 0, nil, errors.New("term: frame too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

func resizePayload(rows, cols int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b, uint16(rows))     // #nosec G115 -- terminal sizes fit
	binary.BigEndian.PutUint16(b[2:], uint16(cols)) // #nosec G115 -- terminal sizes fit
	return b
}

func attachPayload(rows, cols int, mode string) []byte {
	b := make([]byte, 6, 6+len(mode))
	binary.BigEndian.PutUint16(b, protocolVersion)
	copy(b[2:], resizePayload(rows, cols))
	return append(b, mode...)
}

// attachMode is the mode an attach payload asks for: ModeStream when it
// names none.
func attachMode(p []byte) string {
	if len(p) <= 6 {
		return ModeStream
	}
	return string(p[6:])
}

// attachSize reads the uint16 at offset in an attach payload, 0 if absent.
func attachSize(p []byte, offset int) uint16 {
	if len(p) < offset+2 {
		return 0
	}
	return binary.BigEndian.Uint16(p[offset:])
}

// ring keeps the last n bytes written.
type ring struct {
	n   int
	buf []byte
}

func (r *ring) Write(p []byte) {
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - r.n; over > 0 {
		r.buf = append(r.buf[:0:0], r.buf[over:]...)
	}
}

func (r *ring) Bytes() []byte { return append([]byte(nil), r.buf...) }
