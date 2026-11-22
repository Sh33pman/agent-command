package errcode

import "testing"

// Each code is listed once, means something, and is snake_case.
func TestEveryCodeIsListedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All {
		if seen[c.Code] {
			t.Errorf("%s twice", c.Code)
		}
		seen[c.Code] = true
		if c.Meaning == "" {
			t.Errorf("%s means nothing", c.Code)
		}
		for _, r := range c.Code {
			if (r < 'a' || r > 'z') && r != '_' {
				t.Errorf("%s isn't snake_case", c.Code)
				break
			}
		}
		if !Known(c.Code) {
			t.Errorf("%s isn't known", c.Code)
		}
	}
	if Known("failed") || Known("") {
		t.Error("an unlisted code is known")
	}
	if !seen[Internal] || !seen[Conflict] || !seen[BadRequest] {
		t.Error("missing a code")
	}
}
