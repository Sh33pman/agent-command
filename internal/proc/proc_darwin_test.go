//go:build darwin

package proc

import "testing"

func TestTTYPath(t *testing.T) {
	for dev, want := range map[int32]string{-1: "", 0x10000001: "/dev/ttys001", 0x1000002a: "/dev/ttys042", 0x04000001: ""} {
		if got := ttyPath(dev); got != want {
			t.Errorf("ttyPath(%#x) = %q, want %q", dev, got, want)
		}
	}
}

func TestParseProcArgs2(t *testing.T) {
	raw := append([]byte{2, 0, 0, 0}, []byte("/bin/claude\x00\x00\x00claude\x00--resume\x00HOME=/x\x00")...)
	if got := parseProcArgs2(raw); len(got) != 2 || got[0] != "claude" || got[1] != "--resume" {
		t.Errorf("args = %q", got)
	}
	if parseProcArgs2([]byte{1}) != nil || parseProcArgs2([]byte{1, 0, 0, 0, 'x'}) != nil {
		t.Error("short or unterminated")
	}
}
