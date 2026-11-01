//go:build darwin && cgo

package popup

import "testing"

func TestCarbonKey(t *testing.T) {
	code, mods, err := carbonKey("cmd+ctrl+o")
	if err != nil || code != 0x1F || mods != carbonCmd|carbonCtrl {
		t.Errorf("cmd+ctrl+o: %#x %#x %v", code, mods, err)
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
