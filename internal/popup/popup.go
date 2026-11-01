// Package popup is acmd's reply popup (SPEC §8.8): a native macOS panel,
// centered and floating above every app (AeroSpace leaves panels alone),
// that shows sessions' final responses, questions and permission prompts
// and takes a reply. The panel is thin AppKit code (panel_darwin.m); what
// it shows and what its keys do is decided in Go.
package popup

import "errors"

// ErrUnsupported means this build has no native panel (not macOS, or
// built without cgo).
var ErrUnsupported = errors.New("the popup needs macOS (built with cgo)")

// State is what the panel shows.
type State struct {
	Visible     bool     `json:"visible"`
	Title       string   `json:"title"`       // the session's name
	Meta        string   `json:"meta"`        // status · folder
	Counter     string   `json:"counter"`     // "4 of 7"
	Body        string   `json:"body"`        // the response or question
	Options     []string `json:"options"`     // answers to pick (questions, permissions)
	Placeholder string   `json:"placeholder"` // the reply box's hint
	Playing     bool     `json:"playing"`     // speech is playing
	CanReply    bool     `json:"canReply"`    // the reply box is enabled
	Note        string   `json:"note"`        // a line under the buttons (errors, "Sending…")
}

// Binding is one key of the panel: Key is a key name ("left", "return",
// "escape", "pageup", or a single character) and Mods its modifiers
// ("cmd", "ctrl", "alt", "shift").
type Binding struct {
	Action string   `json:"action"`
	Key    string   `json:"key"`
	Mods   []string `json:"mods"`
}

// Event is something the user did in the panel.
type Event struct {
	Kind string // send, option, prev, next, play, skip, close, hotkey
	Text string // send: the reply
	N    int    // option: its index; hotkey: its id
}
