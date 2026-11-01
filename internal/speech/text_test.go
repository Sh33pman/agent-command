package speech

import (
	"strings"
	"testing"
)

func TestPrepare(t *testing.T) {
	md := "## Summary\n\nI **refactored** the `auth` middleware (see [the PR](https://x.y/1)).\n\n" +
		"```go\nfunc main() {}\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- tests pass\n- docs updated\n\nDetails at https://example.com/x."
	got := strings.Join(Prepare(md, 400), " | ")
	want := "Summary. | I refactored the auth middleware (see the PR). | " + codeNote + " | " + tableNote +
		" | tests pass. | docs updated. | Details at a link"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestPrepareChunks(t *testing.T) {
	long := strings.Repeat("This is a sentence that goes on. ", 30)
	chunks := Prepare(long, 120)
	if len(chunks) < 8 {
		t.Fatalf("%d chunks", len(chunks))
	}
	for _, c := range chunks {
		if len(c) > 120 || !strings.HasSuffix(c, ".") {
			t.Errorf("chunk %q (len %d)", c, len(c))
		}
	}
	if got := Prepare(strings.Repeat("x", 250), 100); len(got) != 3 {
		t.Errorf("unbreakable text: %d chunks", len(got))
	}
	if len(Prepare("```\ncode only\n```", 100)) != 1 {
		t.Error("code-only response")
	}
}
