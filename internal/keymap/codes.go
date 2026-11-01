package keymap

import "fmt"

// Ghostty keybinds can't run programs (SPEC §7.3), so in Ghostty an acmd
// shortcut focuses the sidebar (goto_split:left) and sends it a private
// key: a CSI u sequence with a code from Unicode's Private Use Area,
// which the sidebar turns into the action. Kitty runs `acmd <action>`
// instead; the key combos are the same in both terminals.

// codes are the private key codes of the global actions sent to the
// sidebar; go N is goBase+N.
var codes = map[string]rune{
	"new":            0xE0A0,
	"next-attention": 0xE0A1,
	"pick":           0xE0A2,
	"open":           0xE0A3,
	"keys":           0xE0A4,
	"adopt":          0xE0A5,
	"term":           0xE0A6,
}

const goBase rune = 0xE0B0

// Code is the private key code for a global action ("go 3" included);
// false for actions Ghostty does natively (toggle, focus).
func Code(action string) (rune, bool) {
	var n int
	if _, err := fmt.Sscanf(action, "go %d", &n); err == nil && n >= 1 && n <= 9 {
		return goBase + rune(n), true
	}
	c, ok := codes[action]
	return c, ok
}

// Action is the global action for a private key code ("" = not one).
func Action(code rune) string {
	if code > goBase && code <= goBase+9 {
		return fmt.Sprintf("go %d", code-goBase)
	}
	for a, c := range codes {
		if c == code {
			return a
		}
	}
	return ""
}

// CSI is the Ghostty csi: action value that sends code ("57504u").
func CSI(code rune) string { return fmt.Sprintf("%du", code) }
