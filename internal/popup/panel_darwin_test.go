//go:build darwin && cgo

package popup

import "testing"

func TestCarbonKey(t *testing.T) {
	code, mods, err := carbonKey("cmd+shift+o")
	if err != nil || code != 0x1F || mods != carbonCmd|carbonShift {
		t.Errorf("cmd+shift+o: %#x %#x %v", code, mods, err)
	}
	code, mods, err = carbonKey("ctrl+alt+left")
	if err != nil || code != 0x7B || mods != carbonCtrl|carbonAlt {
		t.Errorf("ctrl+alt+left: %#x %#x %v", code, mods, err)
	}
	for _, bad := range []string{"hyper+o", "cmd+shift+F13", ""} {
		if _, _, err := carbonKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
