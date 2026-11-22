package etag

import "testing"

func TestOf(t *testing.T) {
	a, b := Of(map[string]any{"x": 1, "y": "z"}), Of(map[string]any{"y": "z", "x": 1})
	if a == "" || a != b || len(a) != 16 {
		t.Errorf("the same value: %q %q", a, b)
	}
	if Of(map[string]any{"x": 2, "y": "z"}) == a {
		t.Error("a change kept its version")
	}
	if Of(func() {}) != "" {
		t.Error("a value that isn't JSON")
	}
}
