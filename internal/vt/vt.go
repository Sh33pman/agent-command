//go:build libghostty

// Package vt is the host's copy of a session's terminal state, kept by
// libghostty-vt. All calls to that library live
// here, because its API is unstable. Build with -tags libghostty after running
// scripts/build-ghostty.sh; without the tag, Available is false and hosts use
// their raw replay.
package vt

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/ghostty/vt/include
#cgo darwin LDFLAGS: ${SRCDIR}/../../build/ghostty/vt/lib/libghostty-vt.a -lc++
#cgo linux LDFLAGS: ${SRCDIR}/../../build/ghostty/vt/lib/libghostty-vt.a -lstdc++
#include <stdlib.h>
#include <ghostty/vt.h>

extern void acmdWritePty(GhosttyTerminal, void*, uint8_t*, size_t);

static void acmd_write_pty(GhosttyTerminal t, void* userdata, const uint8_t* data, size_t len) {
	acmdWritePty(t, userdata, (uint8_t*)data, len);
}

static void acmd_set_callbacks(GhosttyTerminal t, uintptr_t handle) {
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_USERDATA, (const void*)handle);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void*)acmd_write_pty);
}

static void acmd_set_limits(GhosttyTerminal t, size_t scrollback, size_t continuation) {
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &scrollback);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES, &continuation);
}

static bool acmd_alternate_active(GhosttyTerminal t) {
	GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen);
	return screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

enum { ACMD_TEXT, ACMD_STATE, ACMD_SCREEN };

static GhosttyResult acmd_formatter(GhosttyTerminal t, GhosttyFormatter* out, int kind) {
	GhosttyFormatterTerminalOptions o = {0};
	o.size = sizeof(o);
	o.extra.size = sizeof(o.extra);
	o.extra.screen.size = sizeof(o.extra.screen);
	if (kind == ACMD_TEXT) {
		o.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
		o.trim = true;
		return ghostty_formatter_terminal_new(NULL, out, t, o);
	}
	o.emit = GHOSTTY_FORMATTER_FORMAT_VT;
	o.extra.screen.cursor = true;
	o.extra.screen.style = true;
	if (kind == ACMD_STATE) {
		// Not the palette: the client's theme decides colors.
		o.extra.modes = true;
		o.extra.scrolling_region = true;
		o.extra.tabstops = true;
		o.extra.pwd = true;
		o.extra.keyboard = true;
		o.extra.screen.hyperlink = true;
		o.extra.screen.protection = true;
		o.extra.screen.kitty_keyboard = true;
		o.extra.screen.charsets = true;
	}
	return ghostty_formatter_terminal_new(NULL, out, t, o);
}

// A private copy of t, from its binary snapshot.
static GhosttyResult acmd_clone(GhosttyTerminal t, GhosttyTerminal* out) {
	uint8_t* buf = NULL;
	size_t len = 0;
	GhosttyResult r = ghostty_snapshot_encode_alloc(t, NULL, &buf, &len);
	if (r != GHOSTTY_SUCCESS) return r;
	GhosttySnapshotDecoder d = NULL;
	r = ghostty_snapshot_decoder_new_buf(NULL, &d, buf, len);
	if (r == GHOSTTY_SUCCESS) {
		r = ghostty_snapshot_decoder_decode(d, out);
		ghostty_snapshot_decoder_free(d);
	}
	ghostty_free(NULL, buf, len);
	return r;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"
	"unsafe"
)

// Available reports whether this binary has libghostty-vt.
const Available = true

// maxContinuation limits the unfinished sequence a snapshot can carry over.
const maxContinuation = 1 << 20

// Terminal is one session's terminal state. It is not safe for concurrent use.
type Terminal struct {
	t            C.GhosttyTerminal
	state, plain C.GhosttyFormatter
	handle       cgo.Handle
	reply        func([]byte)
	closed       bool
}

// New makes a terminal. reply receives what the terminal answers to the
// program's queries; it is called during Write, and must not call back into
// the Terminal. scrollbackBytes limits history.
func New(cols, rows, scrollbackBytes int, reply func([]byte)) (*Terminal, error) {
	if cols < 1 || rows < 1 {
		return nil, fmt.Errorf("vt: invalid size %dx%d", cols, rows)
	}
	v := &Terminal{reply: reply}
	if r := C.ghostty_terminal_new(nil, &v.t, C.uint16_t(cols), C.uint16_t(rows)); r != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("vt: new terminal: result %d", int(r))
	}
	v.handle = cgo.NewHandle(v)
	C.acmd_set_callbacks(v.t, C.uintptr_t(v.handle))
	C.acmd_set_limits(v.t, C.size_t(scrollbackBytes), maxContinuation)
	if r := C.acmd_formatter(v.t, &v.state, C.ACMD_STATE); r != C.GHOSTTY_SUCCESS {
		v.Close()
		return nil, fmt.Errorf("vt: state formatter: result %d", int(r))
	}
	if r := C.acmd_formatter(v.t, &v.plain, C.ACMD_TEXT); r != C.GHOSTTY_SUCCESS {
		v.Close()
		return nil, fmt.Errorf("vt: text formatter: result %d", int(r))
	}
	return v, nil
}

//export acmdWritePty
func acmdWritePty(_ C.GhosttyTerminal, userdata unsafe.Pointer, data *C.uint8_t, n C.size_t) {
	v, ok := cgo.Handle(uintptr(userdata)).Value().(*Terminal)
	if !ok || v.reply == nil || n == 0 {
		return
	}
	v.reply(C.GoBytes(unsafe.Pointer(data), C.int(n)))
}

// Write feeds the program's output.
func (v *Terminal) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	C.ghostty_terminal_vt_write(v.t, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
}

// Resize changes the terminal's size.
func (v *Terminal) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return fmt.Errorf("vt: invalid size %dx%d", cols, rows)
	}
	if r := C.ghostty_terminal_resize(v.t, C.uint16_t(cols), C.uint16_t(rows), 0, 0); r != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("vt: resize: result %d", int(r))
	}
	return nil
}

// Snapshot returns VT sequences that recreate the current state in a terminal
// that has just been reset: the primary screen and its history even when the
// alternate screen is active, and any sequence the program is part way through,
// so the rest of it lands correctly.
func (v *Terminal) Snapshot() ([]byte, error) {
	out, err := format(v.state)
	if err != nil {
		return nil, err
	}
	if bool(C.acmd_alternate_active(v.t)) {
		primary, err := v.primaryScreen()
		if err != nil {
			return nil, err
		}
		out = append(primary, out...) // the state then enters the alternate screen
	}
	var p *C.uint8_t
	var n C.size_t
	if r := C.ghostty_terminal_continuation_alloc(v.t, nil, &p, &n); r == C.GHOSTTY_SUCCESS && n > 0 {
		out = append(out, C.GoBytes(unsafe.Pointer(p), C.int(n))...)
		C.ghostty_free(nil, p, n)
	}
	return out, nil
}

// primaryScreen formats the primary screen while the alternate one is active.
// The formatter only sees the active screen and switching screens changes the
// cursors, so this leaves the alternate screen in a private copy.
func (v *Terminal) primaryScreen() ([]byte, error) {
	var clone C.GhosttyTerminal
	if r := C.acmd_clone(v.t, &clone); r != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("vt: copy terminal: result %d", int(r))
	}
	defer C.ghostty_terminal_free(clone)
	// CAN ends any sequence the copy is part way through.
	leave := []byte("\x18\x1b[?1049l")
	C.ghostty_terminal_vt_write(clone, (*C.uint8_t)(unsafe.Pointer(&leave[0])), C.size_t(len(leave)))
	var f C.GhosttyFormatter
	if r := C.acmd_formatter(clone, &f, C.ACMD_SCREEN); r != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("vt: screen formatter: result %d", int(r))
	}
	defer C.ghostty_formatter_free(f)
	return format(f)
}

// Text returns the current screen and history as plain text.
func (v *Terminal) Text() (string, error) {
	b, err := format(v.plain)
	return string(b), err
}

func format(f C.GhosttyFormatter) ([]byte, error) {
	var p *C.uint8_t
	var n C.size_t
	if r := C.ghostty_formatter_format_alloc(f, nil, &p, &n); r != C.GHOSTTY_SUCCESS {
		return nil, errors.New("vt: format failed")
	}
	defer C.ghostty_free(nil, p, n)
	return C.GoBytes(unsafe.Pointer(p), C.int(n)), nil
}

// Close frees the terminal.
func (v *Terminal) Close() {
	if v.closed {
		return
	}
	v.closed = true
	if v.state != nil {
		C.ghostty_formatter_free(v.state)
	}
	if v.plain != nil {
		C.ghostty_formatter_free(v.plain)
	}
	C.ghostty_terminal_free(v.t)
	v.handle.Delete()
}
