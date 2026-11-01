//go:build darwin && cgo

package popup

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -Wall
#cgo LDFLAGS: -framework Cocoa -framework Carbon
#include <stdlib.h>
void acmdPopupRun(void);
void acmdPopupUpdate(const char *json);
void acmdPopupSetBindings(const char *json);
int acmdPopupRegisterHotKey(int keyCode, int mods, int id);
const char *acmdPopupFrame(void);
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// AppKit must run on the main thread: keep the main goroutine there.
func init() { runtime.LockOSThread() }

var (
	mu      sync.Mutex
	handler func(Event)
)

// Supported reports whether this build has the native panel.
func Supported() bool { return true }

// Run starts the panel's application and calls on for every event (on
// AppKit's thread: on must not block). It never returns, and must be
// called from the main goroutine.
func Run(on func(Event)) error {
	mu.Lock()
	handler = on
	mu.Unlock()
	C.acmdPopupRun()
	return nil
}

// Update shows s (or hides the panel); safe from any goroutine.
func Update(s State) {
	if s.Options == nil {
		s.Options = []string{}
	}
	b, _ := json.Marshal(s)
	cs := C.CString(string(b))
	defer C.free(unsafe.Pointer(cs))
	C.acmdPopupUpdate(cs)
}

// SetBindings sets the panel's keys.
func SetBindings(bs []Binding) {
	b, _ := json.Marshal(bs)
	cs := C.CString(string(b))
	defer C.free(unsafe.Pointer(cs))
	C.acmdPopupSetBindings(cs)
}

// RegisterHotKey makes key (e.g. "cmd+shift+o") a system-wide shortcut
// that sends Event{Kind: "hotkey", N: id}. It needs no permission.
func RegisterHotKey(id int, key string) error {
	code, mods, err := carbonKey(key)
	if err != nil {
		return err
	}
	if C.acmdPopupRegisterHotKey(C.int(code), C.int(mods), C.int(id)) != 0 {
		return fmt.Errorf("couldn't register %s (another app may use it)", key)
	}
	return nil
}

//export goPopupEvent
func goPopupEvent(kind, text *C.char, n C.int) {
	mu.Lock()
	h := handler
	mu.Unlock()
	if h != nil {
		h(Event{Kind: C.GoString(kind), Text: C.GoString(text), N: int(n)})
	}
}

// Carbon modifier bits (cmdKey, shiftKey, optionKey, controlKey).
const (
	carbonCmd   = 1 << 8
	carbonShift = 1 << 9
	carbonAlt   = 1 << 11
	carbonCtrl  = 1 << 12
)

// carbonKey turns "cmd+shift+o" into a virtual key code and Carbon
// modifiers.
func carbonKey(key string) (int, int, error) {
	parts := strings.Split(strings.ToLower(key), "+")
	mods := 0
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "cmd", "super", "command":
			mods |= carbonCmd
		case "shift":
			mods |= carbonShift
		case "alt", "opt", "option":
			mods |= carbonAlt
		case "ctrl", "control":
			mods |= carbonCtrl
		default:
			return 0, 0, fmt.Errorf("unknown modifier %q in %q", p, key)
		}
	}
	code, ok := virtualKeys[parts[len(parts)-1]]
	if !ok {
		return 0, 0, fmt.Errorf("unknown key %q in %q", parts[len(parts)-1], key)
	}
	return code, mods, nil
}

// virtualKeys are macOS virtual key codes (ANSI layout, Events.h).
var virtualKeys = map[string]int{
	"a": 0x00, "s": 0x01, "d": 0x02, "f": 0x03, "h": 0x04, "g": 0x05, "z": 0x06, "x": 0x07,
	"c": 0x08, "v": 0x09, "b": 0x0B, "q": 0x0C, "w": 0x0D, "e": 0x0E, "r": 0x0F, "y": 0x10,
	"t": 0x11, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15, "6": 0x16, "5": 0x17, "9": 0x19,
	"7": 0x1A, "8": 0x1C, "0": 0x1D, "o": 0x1F, "u": 0x20, "i": 0x22, "p": 0x23, "l": 0x25,
	"j": 0x26, "k": 0x28, "n": 0x2D, "m": 0x2E, "space": 0x31, "return": 0x24, "escape": 0x35,
	"left": 0x7B, "right": 0x7C, "down": 0x7D, "up": 0x7E,
}

// Frame describes the panel's window (diagnostics; not on AppKit's thread).
func Frame() string {
	cs := C.acmdPopupFrame()
	defer C.free(unsafe.Pointer(cs))
	return C.GoString(cs)
}
