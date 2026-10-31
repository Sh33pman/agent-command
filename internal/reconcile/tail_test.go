package reconcile

import (
	"os"
	"path/filepath"
	"testing"
)

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func check(t *testing.T, tl *Tailer, p string, want bool) {
	t.Helper()
	got, err := tl.Interrupted(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Interrupted = %v, want %v", got, want)
	}
}

func TestTailerFindsNewInterruptMarker(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	// History containing an old interrupt must not trigger.
	appendTo(t, p, `{"type":"user","message":"[Request interrupted by user for tool use]"}`+"\n")
	tl := NewTailer()
	check(t, tl, p, false) // first sight: just records the size
	check(t, tl, p, false) // nothing new
	appendTo(t, p, `{"type":"assistant","message":"working"}`+"\n")
	check(t, tl, p, false)
	appendTo(t, p, `{"type":"user","message":"[Request interrupted by user for tool use]"}`+"\n")
	check(t, tl, p, true)
	check(t, tl, p, false)
}

func TestTailerMarkerSplitAcrossChecks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	appendTo(t, p, "x\n")
	tl := NewTailer()
	check(t, tl, p, false)
	appendTo(t, p, `{"m":"[Request interr`)
	check(t, tl, p, false)
	appendTo(t, p, `upted by user]"}`+"\n")
	check(t, tl, p, true)
}

func TestTailerEdgeCases(t *testing.T) {
	tl := NewTailer()
	check(t, tl, "", false)
	check(t, tl, filepath.Join(t.TempDir(), "missing.jsonl"), false)

	p := filepath.Join(t.TempDir(), "s.jsonl")
	appendTo(t, p, "a long line of history\n")
	check(t, tl, p, false)
	// Truncated/rewritten (e.g. compaction): no false positive, offset resets.
	if err := os.WriteFile(p, []byte("[Request interrupted by user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(t, tl, p, false)
	appendTo(t, p, "[Request interrupted by user]\n")
	check(t, tl, p, true)

	tl.Forget(p)
	appendTo(t, p, "[Request interrupted by user]\n")
	check(t, tl, p, false) // forgotten: first sight again
}
