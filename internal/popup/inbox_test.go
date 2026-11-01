package popup

import (
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

func items(ids ...string) []core.InboxItem {
	var out []core.InboxItem
	for i, id := range ids {
		out = append(out, core.InboxItem{SessionID: id, Kind: core.InboxDone, Text: "text " + id, At: time.Unix(int64(i), 0)})
	}
	return out
}

func TestInboxNavigation(t *testing.T) {
	sessions := map[string]*core.Session{"a": {ID: "a", Name: "auth", Status: core.StatusDone, Cwd: "/home/w/api"}}
	b := &Inbox{Home: "/home"}
	fresh := b.Set(items("a", "b", "c"), sessions)
	if strings.Join(fresh, ",") != "a,b,c" || b.Current != "a" || b.Open {
		t.Fatalf("set: %v %q %v", fresh, b.Current, b.Open)
	}
	if b.State().Visible {
		t.Error("visible before Show")
	}
	b.Show("")
	s := b.State()
	if !s.Visible || s.Counter != "1 of 3" || s.Title != "auth" || s.Meta != "done · ~/w/api" || !s.CanReply || s.Body != "text a" {
		t.Errorf("state: %+v", s)
	}
	b.Move(-1)
	if b.State().Counter != "3 of 3" {
		t.Errorf("wrap back: %s", b.State().Counter)
	}
	b.Move(1)
	b.Move(1)
	if b.Current != "b" || b.State().Counter != "2 of 3" {
		t.Errorf("forward: %s", b.Current)
	}

	// New snapshot: b's item updated, c gone, d new; b stays shown.
	next := items("a", "b", "d")
	next[1].Text, next[1].At = "newer", time.Unix(10, 0)
	fresh = b.Set(next, sessions)
	if strings.Join(fresh, ",") != "b,d" || b.Current != "b" {
		t.Errorf("update: %v %q", fresh, b.Current)
	}
	// The shown item disappears: the first is shown.
	b.Set(items("a", "d"), sessions)
	if b.Current != "a" {
		t.Errorf("after the shown one went: %q", b.Current)
	}

	// Skip/send removes it and shows the next; the last one closes.
	b.Remove("a")
	if b.Current != "d" || !b.Open || b.State().Counter != "1 of 1" {
		t.Errorf("remove: %q %v", b.Current, b.Open)
	}
	b.Remove("d")
	if b.Open || b.State().Visible {
		t.Error("still open with nothing left")
	}
}

func TestInboxKinds(t *testing.T) {
	b := &Inbox{}
	b.Set([]core.InboxItem{
		{SessionID: "q", Kind: core.InboxQuestion, Text: "Red or blue?", Options: []string{"Red", "Blue"}},
		{SessionID: "p", Kind: core.InboxPermission, Text: "Claude wants to use Bash", Options: core.PermissionOptions},
	}, nil)
	b.Show("q")
	if s := b.State(); !s.CanReply || len(s.Options) != 2 || !strings.HasPrefix(s.Meta, "asks you") {
		t.Errorf("question: %+v", s)
	}
	b.Show("p")
	if s := b.State(); s.CanReply || len(s.Options) != 3 || !strings.HasPrefix(s.Meta, "wants permission") {
		t.Errorf("permission: %+v", s)
	}
}

func TestInboxReplyResets(t *testing.T) {
	b := &Inbox{}
	b.Set(items("a", "b"), nil)
	b.Show("a")
	r := b.State().Reply
	b.Sent()
	if b.State().Reply == r {
		t.Error("sending didn't empty the reply box")
	}
	r = b.State().Reply
	b.Move(1)
	if b.State().Reply == r {
		t.Error("another item didn't empty the reply box")
	}
	r = b.State().Reply
	b.Remove("b")
	if b.State().Reply == r {
		t.Error("removing the shown item didn't empty the reply box")
	}
}
