package daemon

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// sent records what's typed into sessions.
type sent struct {
	mu   sync.Mutex
	keys []string
}

func (s *sent) send(_ string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, string(data))
	return nil
}

func (s *sent) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.keys, "|")
}

// inboxSetup starts a daemon with two hosted sessions, and sends events to them.
func inboxSetup(t *testing.T) (*ipc.Conn, *sent, *ipc.Session, *ipc.Session, func(*ipc.Session, string, string)) {
	t.Helper()
	p := testPaths(t)
	typed := &sent{}
	start(t, p, func(c *Config) {
		c.Hosts = &fakeHosts{paths: p}
		c.SendToHost = typed.send
	})
	c := dial(t, p)
	a := newSess(t, c, ipc.NewRequest{Cwd: "/w/a"})
	b := newSess(t, c, ipc.NewRequest{Cwd: "/w/b"})
	at := time.Now().Add(time.Hour)
	send := func(s *ipc.Session, name, payload string) {
		t.Helper()
		at = at.Add(time.Second)
		payload = `{"session_id":"` + s.ClaudeSessionID + `",` + payload + `}`
		must(t, c.Call(ipc.OpEvent, hookEvent(name, payload, at, ipc.HookContext{HostSession: s.ID}), nil))
	}
	return c, typed, a, b, send
}

const (
	finished   = `"last_assistant_message":"Done."`
	permission = `"tool_name":"Bash","tool_input":{"command":"make"}`
)

// The queue holds each session's latest item, and a new one goes to the back.
func TestInboxQueueOrderAndLatestOnly(t *testing.T) {
	c, _, a, b, send := inboxSetup(t)
	send(a, "Stop", `"last_assistant_message":"First."`)
	send(b, "Stop", `"last_assistant_message":"Other."`)
	send(a, "Stop", `"last_assistant_message":"Second."`)

	q := snapshot(t, c).Inbox
	if len(q) != 2 || q[0].SessionID != b.ID || q[1].SessionID != a.ID || q[1].Text != "Second." {
		t.Fatalf("queue: %+v", q)
	}
	history := inboxOf(t, c, ipc.InboxRequest{SessionID: a.ID})
	if len(history) != 2 || history[0].Text != "Second." || history[1].Text != "First." || history[1].State != core.InboxStale {
		t.Errorf("a's history: %+v", history)
	}
	history = inboxOf(t, c, ipc.InboxRequest{})
	if len(history) != 3 {
		t.Errorf("all: %+v", history)
	}

	// Page by page, newest first: a new item meanwhile doesn't shift them.
	var page ipc.InboxPage
	must(t, c.Call(ipc.OpInbox, ipc.InboxRequest{Page: ipc.Page{Limit: 2}}, &page))
	if len(page.Items) != 2 || page.Items[0].ID != history[0].ID || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	send(b, "Stop", `"last_assistant_message":"Newer."`)
	must(t, c.Call(ipc.OpInbox, ipc.InboxRequest{Page: ipc.Page{Limit: 2, Cursor: page.NextCursor}}, &page))
	if len(page.Items) != 1 || page.Items[0].ID != history[2].ID || page.NextCursor != "" {
		t.Errorf("last page = %+v", page)
	}
	for _, bad := range []ipc.InboxRequest{{Page: ipc.Page{Cursor: "nope"}}, {Page: ipc.Page{Limit: -1}}} {
		if err := c.Call(ipc.OpInbox, bad, nil); codeOf(err) != errcode.BadRequest {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

// Reading keeps an item in the queue; dismissing or the session moving on removes it.
func TestInboxReadDismissAndMovingOn(t *testing.T) {
	c, _, a, b, send := inboxSetup(t)
	send(a, "Stop", finished)
	send(b, "PermissionRequest", permission)
	q := snapshot(t, c).Inbox
	if q[0].Read {
		t.Fatalf("new item is read: %+v", q[0])
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutInboxRead, Items: ipc.HereItems(q[0].ID), Flag: true})
	if got := snapshot(t, c).Inbox; len(got) != 2 || !got[0].Read {
		t.Errorf("after read: %+v", got)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutInboxRead, Items: ipc.HereItems(q[0].ID)})
	if got := snapshot(t, c).Inbox; got[0].Read {
		t.Errorf("after unread: %+v", got)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutInboxDismiss, Items: ipc.HereItems(q[0].ID)})
	send(b, "PreToolUse", `"tool_name":"Bash"`)
	if got := snapshot(t, c).Inbox; len(got) != 0 {
		t.Errorf("queue: %+v", got)
	}
	history := inboxOf(t, c, ipc.InboxRequest{})
	if history[0].State != core.InboxStale || history[1].State != core.InboxDismissed {
		t.Errorf("history: %+v", history)
	}
	// A late dismiss doesn't change what already happened.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutInboxDismiss, Items: ipc.HereItems(history[0].ID)})
	history = inboxOf(t, c, ipc.InboxRequest{})
	if history[0].State != core.InboxStale {
		t.Errorf("late dismiss: %+v", history[0])
	}
}

// Answers are typed only when the session is waiting for one.
func TestInboxAnswers(t *testing.T) {
	c, typed, a, _, send := inboxSetup(t)
	answer := func(a ipc.InboxAnswer) error { return c.Call(ipc.OpInboxAnswer, a, nil) }

	// A permission takes a number key.
	send(a, "PermissionRequest", permission)
	item := snapshot(t, c).Inbox[0]
	if err := answer(ipc.InboxAnswer{ID: item.ID, Option: 4}); err == nil {
		t.Error("answered with an option that doesn't exist")
	}
	must(t, answer(ipc.InboxAnswer{ID: item.ID, Option: 2}))
	if typed.all() != "2" {
		t.Errorf("typed %q", typed.all())
	}
	if got := snapshot(t, c).Inbox; len(got) != 0 {
		t.Errorf("queue: %+v", got)
	}
	if err := answer(ipc.InboxAnswer{ID: item.ID, Option: 1}); err == nil {
		t.Error("answered a handled item")
	}

	// While Claude is busy, nothing is typed.
	typed.keys = nil
	send(a, "Stop", finished)
	item = snapshot(t, c).Inbox[0]
	send(a, "UserPromptSubmit", `"prompt":"go"`) // stale, and busy
	if err := answer(ipc.InboxAnswer{ID: item.ID, Text: "more"}); err == nil {
		t.Error("answered while busy")
	}

	// A reply is pasted, and its Enter sent separately. An escape can't end the paste.
	send(a, "Stop", finished)
	item = snapshot(t, c).Inbox[0]
	if err := answer(ipc.InboxAnswer{ID: item.ID, Text: "  "}); err == nil {
		t.Error("sent an empty reply")
	}
	must(t, answer(ipc.InboxAnswer{ID: item.ID, Text: "more\x1b[201~"}))
	if typed.all() != "\x1b[200~more[201~\x1b[201~|\r" {
		t.Errorf("typed %q", typed.all())
	}
	history := inboxOf(t, c, ipc.InboxRequest{SessionID: a.ID})
	if history[0].State != core.InboxResponded || !history[0].Read {
		t.Errorf("after reply: %+v", history[0])
	}
}

// A question with several parts can't be answered from the inbox.
func TestInboxMultipartQuestionsGoToTheSession(t *testing.T) {
	c, typed, a, _, send := inboxSetup(t)
	send(a, "PermissionRequest", `"tool_name":"AskUserQuestion","tool_input":{"questions":[`+
		`{"question":"Color?","options":[{"label":"Red"}]},{"question":"Shape?","options":[{"label":"Round"}]}]}`)
	item := snapshot(t, c).Inbox[0]
	if !item.Multipart || len(item.Options) != 0 {
		t.Fatalf("item: %+v", item)
	}
	if err := c.Call(ipc.OpInboxAnswer, ipc.InboxAnswer{ID: item.ID, Option: 1}, nil); err == nil || typed.all() != "" {
		t.Errorf("answered: %v, typed %q", err, typed.all())
	}
}

// Archiving a session closes its items, which stay in the history.
func TestInboxArchiveClosesItems(t *testing.T) {
	c, _, a, _, send := inboxSetup(t)
	send(a, "Stop", finished)
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(a.ID)})
	if got := snapshot(t, c).Inbox; len(got) != 0 {
		t.Errorf("queue: %+v", got)
	}
	history := inboxOf(t, c, ipc.InboxRequest{SessionID: a.ID})
	if len(history) != 1 || history[0].State != core.InboxStale {
		t.Errorf("history: %+v", history)
	}
}
