//go:build darwin

package hook

import "testing"

func TestTTYPath(t *testing.T) {
	for dev, want := range map[int32]string{-1: "", 0x10000001: "/dev/ttys001", 0x1000002a: "/dev/ttys042", 0x04000001: ""} {
		if got := ttyPath(dev); got != want {
			t.Errorf("ttyPath(%#x) = %q, want %q", dev, got, want)
		}
	}
}
