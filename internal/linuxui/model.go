//go:build linux

package linuxui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// SelectionKind identifies what the content pane is currently showing.
type SelectionKind int

const (
	SelectNone SelectionKind = iota
	SelectSession
	SelectHarness
)

// Selection identifies the active item in the sidebar.
type Selection struct {
	Kind SelectionKind
	ID   string
	Host string
}

// SessionKey uniquely identifies a session across hosts. It avoids NUL bytes
// because it is also used as a GtkStack child name in C.
func SessionKey(host, id string) string {
	if host == "" {
		return "local:" + id
	}
	return host + ":" + id
}

// SidebarModel holds the computed sidebar rows and harnesses for rendering.
type SidebarModel struct {
	Rows      []core.Row
	Harnesses []ipc.Harness
	Sessions  map[string]*ipc.Session // keyed by SessionKey(host, id)
}

// BuildSidebar computes the sidebar rows using core.Layout and indexes sessions by (host, id).
func BuildSidebar(snap ipc.Snapshot, filter string) SidebarModel {
	vs := snap.ViewState.Core()
	if strings.TrimSpace(filter) != "" {
		vs.Filter = filter
	}
	rows := core.Layout(snap.CoreSessions(), snap.CoreGroups(), vs)
	byKey := make(map[string]*ipc.Session, len(snap.Sessions))
	for _, s := range snap.Sessions {
		byKey[SessionKey(s.Host, s.ID)] = s
	}
	return SidebarModel{
		Rows:      rows,
		Harnesses: snap.Harnesses,
		Sessions:  byKey,
	}
}

// FindHarness returns the harness with the given ID from the snapshot.
func FindHarness(snap ipc.Snapshot, id string) (ipc.Harness, bool) {
	for _, h := range snap.Harnesses {
		if h.ID == id {
			return h, true
		}
	}
	return ipc.Harness{}, false
}

// FindSession returns the session with the given ID and host from the snapshot.
func FindSession(snap ipc.Snapshot, id, host string) (*ipc.Session, bool) {
	for _, s := range snap.Sessions {
		if s.ID == id && s.Host == host {
			return s, true
		}
	}
	return nil, false
}

// NoticesForClient filters notices targeting clientID (or broadcast when To is empty)
// with Seq > afterSeq, returning the matching notices and the new highest Seq.
func NoticesForClient(notices []ipc.Notice, clientID string, afterSeq uint64) ([]ipc.Notice, uint64) {
	var out []ipc.Notice
	maxSeq := afterSeq
	for _, n := range notices {
		if n.Seq <= afterSeq {
			continue
		}
		if n.Seq > maxSeq {
			maxSeq = n.Seq
		}
		if len(n.To) == 0 || containsString(n.To, clientID) {
			out = append(out, n)
		}
	}
	return out, maxSeq
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// ResolveACMD finds the acmd binary next to the current executable or on PATH.
func ResolveACMD() (string, error) {
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"acmd", "agent-command"} {
			p := filepath.Join(filepath.Dir(exe), name)
			if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return p, nil
			}
		}
	}
	for _, name := range []string{"acmd", "agent-command"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("acmd binary not found next to agent-command-ui or on PATH")
}

// AttachArgv returns the argv VTE should spawn to attach to a session.
func AttachArgv(acmdBin, sessionID string) []string {
	return []string{acmdBin, "host", "attach", "--session", sessionID}
}

// StatusLabel returns a short human-readable label for a session status.
func StatusLabel(s core.Status) string {
	switch s {
	case core.StatusRunning:
		return "Running"
	case core.StatusWaiting:
		return "Waiting"
	case core.StatusError:
		return "Error"
	case core.StatusDone:
		return "Done"
	case core.StatusPaused:
		return "Paused"
	case core.StatusArchived:
		return "Archived"
	default:
		return string(s)
	}
}

// RGB represents an sRGB color in 0..1 float32 range for GdkRGBA.
type RGB struct {
	R, G, B float32
}

// Palette holds terminal foreground, background, and 16 ANSI colors.
type Palette struct {
	Foreground RGB
	Background RGB
	Colors     [16]RGB
}

// DefaultPalette is a dark Adwaita-friendly terminal palette.
var DefaultPalette = Palette{
	Foreground: RGB{0.93, 0.93, 0.95},
	Background: RGB{0.11, 0.11, 0.13},
	Colors: [16]RGB{
		{0.15, 0.16, 0.19}, // 0 black
		{0.88, 0.42, 0.46}, // 1 red
		{0.60, 0.76, 0.47}, // 2 green
		{0.90, 0.75, 0.48}, // 3 yellow
		{0.51, 0.63, 0.76}, // 4 blue
		{0.71, 0.56, 0.68}, // 5 magenta
		{0.53, 0.75, 0.82}, // 6 cyan
		{0.90, 0.91, 0.94}, // 7 white
		{0.30, 0.34, 0.42}, // 8 bright black
		{0.92, 0.52, 0.55}, // 9 bright red
		{0.68, 0.84, 0.55}, // 10 bright green
		{0.95, 0.82, 0.56}, // 11 bright yellow
		{0.60, 0.72, 0.85}, // 12 bright blue
		{0.80, 0.65, 0.77}, // 13 bright magenta
		{0.61, 0.83, 0.89}, // 14 bright cyan
		{0.96, 0.97, 0.98}, // 15 bright white
	},
}

// ParseHexColor parses "#RRGGBB" or "RRGGBB" into RGB.
func ParseHexColor(spec string) (RGB, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(spec), "#")
	if len(s) != 6 {
		return RGB{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{
		R: float32((v>>16)&0xff) / 255.0,
		G: float32((v>>8)&0xff) / 255.0,
		B: float32(v&0xff) / 255.0,
	}, true
}

// ParseGhosttyTheme parses a Ghostty theme file (palette = N=#RRGGBB, foreground, background).
func ParseGhosttyTheme(content string) Palette {
	p := DefaultPalette
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "foreground":
			if c, ok := ParseHexColor(v); ok {
				p.Foreground = c
			}
		case "background":
			if c, ok := ParseHexColor(v); ok {
				p.Background = c
			}
		case "palette":
			idxStr, hexStr, ok := strings.Cut(v, "=")
			if !ok {
				continue
			}
			idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
			if err != nil || idx < 0 || idx >= 16 {
				continue
			}
			if c, ok := ParseHexColor(hexStr); ok {
				p.Colors[idx] = c
			}
		}
	}
	return p
}
