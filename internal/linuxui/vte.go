//go:build linux && !noui

package linuxui

/*
#cgo pkg-config: vte-2.91-gtk4 gtk4
#include <stdlib.h>
#include <gtk/gtk.h>
#include <vte/vte.h>

static GtkWidget* acmd_vte_new(void) {
	return vte_terminal_new();
}

static void acmd_vte_spawn(
	VteTerminal *term,
	const char *cwd,
	char **argv,
	char **envv
) {
	vte_terminal_spawn_async(
		term,
		VTE_PTY_DEFAULT,
		cwd,
		argv,
		envv,
		G_SPAWN_SEARCH_PATH,
		NULL, NULL, NULL,
		-1,
		NULL,
		NULL, NULL
	);
}

static void acmd_vte_set_colors(
	VteTerminal *term,
	const GdkRGBA *fg,
	const GdkRGBA *bg,
	const GdkRGBA *palette,
	gsize palette_size
) {
	vte_terminal_set_colors(term, fg, bg, palette, palette_size);
}

static void acmd_vte_set_font(VteTerminal *term, const char *desc_str) {
	PangoFontDescription *desc = pango_font_description_from_string(desc_str);
	vte_terminal_set_font(term, desc);
	pango_font_description_free(desc);
}

static void acmd_vte_feed_child(VteTerminal *term, const char *data, gssize len) {
	vte_terminal_feed_child(term, data, len);
}

static void acmd_vte_copy(VteTerminal *term) {
	vte_terminal_copy_clipboard_format(term, VTE_FORMAT_TEXT);
}

static void acmd_vte_paste(VteTerminal *term) {
	vte_terminal_paste_clipboard(term);
}
*/
import "C"

import (
	"unsafe"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// VTETerminal wraps a GTK4 VteTerminal widget.
type VTETerminal struct {
	ptr    *C.VteTerminal
	widget *gtk.Widget
}

// NewVTETerminal creates a new VTE terminal widget with sensible defaults.
func NewVTETerminal() *VTETerminal {
	raw := C.acmd_vte_new()
	obj := glib.Take(unsafe.Pointer(raw))
	w := &gtk.Widget{
		InitiallyUnowned: glib.InitiallyUnowned{Object: obj},
		Object:           obj,
		Accessible:       gtk.Accessible{Object: obj},
		Buildable:        gtk.Buildable{Object: obj},
		ConstraintTarget: gtk.ConstraintTarget{Object: obj},
	}
	w.SetHExpand(true)
	w.SetVExpand(true)
	w.SetFocusable(true)

	term := (*C.VteTerminal)(unsafe.Pointer(raw))
	C.vte_terminal_set_scrollback_lines(term, 10000)
	C.vte_terminal_set_scroll_on_output(term, C.FALSE)
	C.vte_terminal_set_mouse_autohide(term, C.TRUE)

	vt := &VTETerminal{
		ptr:    term,
		widget: w,
	}
	vt.SetPalette(DefaultPalette)
	return vt
}

// Widget returns the underlying *gtk.Widget.
func (v *VTETerminal) Widget() *gtk.Widget {
	return v.widget
}

// SpawnAsync spawns argv in cwd inside the VTE PTY.
func (v *VTETerminal) SpawnAsync(cwd string, argv []string) {
	var cCwd *C.char
	if cwd != "" {
		cCwd = C.CString(cwd)
		defer C.free(unsafe.Pointer(cCwd))
	}
	cArgv := makeCStringArray(argv)
	defer freeCStringArray(cArgv)

	C.acmd_vte_spawn(v.ptr, cCwd, (**C.char)(unsafe.Pointer(&cArgv[0])), nil)
}

// FeedChild sends raw bytes to the terminal child process.
func (v *VTETerminal) FeedChild(data []byte) {
	if len(data) == 0 {
		return
	}
	cData := C.CBytes(data)
	defer C.free(cData)
	C.acmd_vte_feed_child(v.ptr, (*C.char)(cData), C.gssize(len(data)))
}

// SetPalette applies fg, bg, and 16 ANSI colors to the VTE terminal.
func (v *VTETerminal) SetPalette(p Palette) {
	fg := toGdkRGBA(p.Foreground)
	bg := toGdkRGBA(p.Background)
	var pal [16]C.GdkRGBA
	for i := range p.Colors {
		pal[i] = toGdkRGBA(p.Colors[i])
	}
	C.acmd_vte_set_colors(v.ptr, &fg, &bg, &pal[0], 16)
}

// SetFont sets the terminal font from a Pango font description string (e.g. "Monospace 11").
func (v *VTETerminal) SetFont(desc string) {
	cDesc := C.CString(desc)
	defer C.free(unsafe.Pointer(cDesc))
	C.acmd_vte_set_font(v.ptr, cDesc)
}

// CopyClipboard copies the selection to the clipboard.
func (v *VTETerminal) CopyClipboard() {
	C.acmd_vte_copy(v.ptr)
}

// PasteClipboard pastes from the clipboard.
func (v *VTETerminal) PasteClipboard() {
	C.acmd_vte_paste(v.ptr)
}

// GrabFocus focuses the terminal widget.
func (v *VTETerminal) GrabFocus() {
	v.widget.GrabFocus()
}

// ConnectChildExited registers a callback invoked when the child process exits.
func (v *VTETerminal) ConnectChildExited(fn func(status int)) glib.SignalHandle {
	return v.widget.Connect("child-exited", func(_ *gtk.Widget, status int) {
		if fn != nil {
			fn(status)
		}
	})
}

func toGdkRGBA(c RGB) C.GdkRGBA {
	return C.GdkRGBA{
		red:   C.float(c.R),
		green: C.float(c.G),
		blue:  C.float(c.B),
		alpha: 1.0,
	}
}

func makeCStringArray(ss []string) []*C.char {
	out := make([]*C.char, len(ss)+1)
	for i, s := range ss {
		out[i] = C.CString(s)
	}
	out[len(ss)] = nil
	return out
}

func freeCStringArray(arr []*C.char) {
	for _, p := range arr {
		if p != nil {
			C.free(unsafe.Pointer(p))
		}
	}
}
