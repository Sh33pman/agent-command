package popup

import (
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

func TestLatestText(t *testing.T) {
	t0 := time.Now()
	done := core.InboxItem{SessionID: "a", Kind: core.InboxDone, Text: "finished", At: t0}
	q := core.InboxItem{SessionID: "a", Kind: core.InboxQuestion, Text: "which?", At: t0.Add(time.Second)}
	waiting := &core.Session{Status: core.StatusWaiting}
	busy := &core.Session{Status: core.StatusBusy}

	var l Latest
	if _, ok := l.Text("a", waiting); ok {
		t.Fatal("empty Latest has text")
	}
	l.Observe([]core.InboxItem{done})
	if got, _ := l.Text("a", busy); got != "finished" {
		t.Fatalf("done: %q", got)
	}
	l.Observe([]core.InboxItem{q})
	if got, _ := l.Text("a", waiting); got != "which?" {
		t.Fatalf("waiting question: %q", got)
	}
	if got, _ := l.Text("a", busy); got != "finished" {
		t.Fatalf("answered question falls back to the last reply: %q", got)
	}
	if _, ok := l.Text("b", waiting); ok {
		t.Fatal("other session has text")
	}
}
