// Package term keeps a shell alive per session for the terminal pane
// under a view's Claude pane (SPEC §8.7). A server runs the shell in a
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
// with the screen as plain text and close (ScreenText).
//
// Hosts outlive upgrades, so a client can meet a server older or newer than
// itself. Add to this protocol, don't change it: a server ignores frames it
// doesn't know, and a client still works if an attach payload is ignored.
// The attach payload is the protocol version, rows and cols as uint16 BE, and
// may be empty.
package term

import (
	"encoding/binary"
	"errors"
	"io"
)

// Frame types (client to server).
const (
	frameAttach = 'a' // first frame of a client that wants the output
	frameInput  = 'i'
	frameResize = 'r'
	frameKill   = 'k'
	frameWrite  = 'w' // input from a connection that doesn't attach (Send)
	frameText   = 't' // the screen as plain text (ScreenText)
)

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

func attachPayload(rows, cols int) []byte {
	b := make([]byte, 6)
	binary.BigEndian.PutUint16(b, protocolVersion)
	copy(b[2:], resizePayload(rows, cols))
	return b
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
