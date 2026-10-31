package core

import (
	"strings"
	"testing"
)

func TestNameFromPrompt(t *testing.T) {
	tests := map[string]string{
		"":                                "",
		"  fix the bug  ":                 "fix the bug",
		"first line\nsecond line":         "first line",
		"tabs\tand   spaces":              "tabs and spaces",
		"\x1b[31mred\x1b[0m":              "[31mred[0m",
		strings.Repeat("a", 40):           strings.Repeat("a", 40),
		strings.Repeat("a", 41):           strings.Repeat("a", 39) + "…",
		strings.Repeat("é", 50):           strings.Repeat("é", 39) + "…",
		"word " + strings.Repeat("b", 50): "word " + strings.Repeat("b", 34) + "…",
	}
	for in, want := range tests {
		if got := NameFromPrompt(in); got != want {
			t.Errorf("NameFromPrompt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultName(t *testing.T) {
	tests := []struct {
		title, prompt, cwd string
		n                  int
		want               string
	}{
		{"red-blue-preference-question", "Use the tool", "/w", 1, "red-blue-preference-question"},
		{"", "fix the flaky test", "/w/api", 1, "fix the flaky test"},
		{"", "", "/Users/me/api-server/", 1, "api-server"},
		{"", "", "/", 3, "session 3"},
		{"", "", "", 4, "session 4"},
		{"\x1b\x07\x00", "", "/w/x", 1, "x"},
	}
	for _, tt := range tests {
		if got := DefaultName(tt.title, tt.prompt, tt.cwd, tt.n); got != tt.want {
			t.Errorf("DefaultName(%q,%q,%q,%d) = %q, want %q", tt.title, tt.prompt, tt.cwd, tt.n, got, tt.want)
		}
	}
}
