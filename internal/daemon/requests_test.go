package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/catalog"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
)

func codeOf(err error) string {
	var re *ipc.RemoteError
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

func sessionCount(t *testing.T, c *ipc.Conn) int {
	t.Helper()
	return len(snapshotOf(t, c).Sessions)
}

// A creating op with a request id runs once: a retry, a retry at the same
// time, and a retry after the daemon restarts all get the first result.
// Past the window, or after a failure, it runs again.
func TestARequestIDRunsOnce(t *testing.T) {
	p := testPaths(t)
	var later atomic.Int64 // how far the clock has moved on
	now := func() time.Time { return time.Now().Add(time.Duration(later.Load())) }
	fh := &fakeHosts{paths: p}
	r := start(t, p, func(c *Config) { c.Now = now; c.Hosts = fh })
	c := dial(t, p)
	newReq := ipc.NewRequest{Cwd: t.TempDir(), HarnessKind: core.HarnessTerminal, Name: "api"}

	var first, again ipc.Session
	must(t, c.CallOnce(ipc.OpNew, "r1", newReq, &first))
	must(t, c.CallOnce(ipc.OpNew, "r1", newReq, &again))
	if first.ID == "" || again.ID != first.ID || sessionCount(t, c) != 1 {
		t.Fatalf("a retry started another: %s %s, %d sessions", first.ID, again.ID, sessionCount(t, c))
	}

	// Retries at the same time: one runs, the others wait for it.
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var s ipc.Session
			if err := dial(t, p).CallOnce(ipc.OpNew, "r2", newReq, &s); err != nil {
				t.Error(err)
			}
			ids[i] = s.ID
		}()
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Errorf("at once: %v", ids)
		}
	}
	if n := sessionCount(t, c); n != 2 {
		t.Errorf("%d sessions after the retries at once", n)
	}

	// The same id for another op is a mistake.
	err := c.CallOnce(ipc.OpMutate, "r1", ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, nil)
	if codeOf(err) != errcode.BadRequest {
		t.Errorf("another op: %v", err)
	}

	// A failure isn't kept: trying again tries again.
	if err := c.CallOnce(ipc.OpNew, "r3", ipc.NewRequest{HarnessKind: core.HarnessTerminal}, nil); codeOf(err) != errcode.BadRequest {
		t.Fatalf("no folder: %v", err)
	}
	var third ipc.Session
	must(t, c.CallOnce(ipc.OpNew, "r3", newReq, &third))
	if third.ID == "" || third.ID == first.ID {
		t.Errorf("after the failure: %+v", third)
	}

	// Groups too, by their mutation kind.
	var g1, g2 ipc.Group
	must(t, c.CallOnce(ipc.OpMutate, "g1", ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g1))
	must(t, c.CallOnce(ipc.OpMutate, "g1", ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g2))
	if g1.ID == "" || g2.ID != g1.ID {
		t.Errorf("group = %s, %s", g1.ID, g2.ID)
	}

	// An op that creates nothing ignores the id: renaming twice renames twice.
	must(t, c.CallOnce(ipc.OpMutate, "n1", ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(first.ID), Text: "one"}, nil))
	must(t, c.CallOnce(ipc.OpMutate, "n1", ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(first.ID), Text: "two"}, nil))
	for _, s := range snapshotOf(t, c).Sessions {
		if s.ID == first.ID && s.Name != "two" {
			t.Errorf("rename with an id = %q", s.Name)
		}
	}

	// After a restart, the result is still there.
	r.cancel()
	<-r.Done
	start(t, p, func(c *Config) { c.Now = now; c.Hosts = fh })
	c = dial(t, p)
	must(t, c.CallOnce(ipc.OpNew, "r1", newReq, &again))
	if again.ID != first.ID {
		t.Errorf("after a restart: %s, want %s", again.ID, first.ID)
	}

	// Past the window, it's a new request.
	later.Store(int64(ipc.RequestKeep + time.Minute))
	must(t, c.CallOnce(ipc.OpNew, "r1", newReq, &again))
	if again.ID == first.ID {
		t.Error("an id past its window got the old result")
	}
}

// Through a paired device (Direct Connect), the request id comes with the
// call, and the host runs the op once.
func TestARequestIDRunsOnceForAPairedDevice(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	b := hostBackend{d}
	ctx := ipc.WithRequestID(hostapi.WithCaller(context.Background(), catalog.Caller{Scope: catalog.ScopeAct}), "p1")
	in := json.RawMessage(`{"cwd":"` + t.TempDir() + `","harness":"terminal","name":"api"}`)
	var ids []string
	for range 2 {
		raw, err := b.Call(ctx, ipc.OpNew, in)
		must(t, err)
		var s ipc.Session
		must(t, json.Unmarshal(raw, &s))
		ids = append(ids, s.ID)
	}
	if ids[0] == "" || ids[1] != ids[0] {
		t.Errorf("ids = %v", ids)
	}
	raw, err := b.Call(hostapi.WithCaller(context.Background(), catalog.Caller{Scope: catalog.ScopeAct}), ipc.OpNew, in)
	must(t, err)
	var s ipc.Session
	must(t, json.Unmarshal(raw, &s))
	if s.ID == ids[0] {
		t.Error("with no request id, it's a new session")
	}
}

// Every error has a code from the one list: a client decides from it.
func TestEveryErrorHasACode(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	cases := map[string]error{
		errcode.UnknownOp:  c.Call("no.such.op", nil, nil),
		errcode.BadRequest: c.Call(ipc.OpMutate, "not a mutation", nil),
		errcode.NotFound:   c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here("nope"), Text: "x"}, nil),
	}
	for want, err := range cases {
		if got := codeOf(err); got != want {
			t.Errorf("want %s: %v (code %q)", want, err, got)
		}
	}
	// One with no code of its own is internal, never none: this daemon has
	// no terminals to start one in.
	err := c.Call(ipc.OpNew, ipc.NewRequest{Cwd: t.TempDir(), HarnessKind: core.HarnessTerminal}, nil)
	if got := codeOf(err); got != errcode.Internal {
		t.Errorf("new with no terminals: code %q (%v)", got, err)
	}
}

// inboxOf is the first page of the Inbox as r asks.
func inboxOf(t *testing.T, c *ipc.Conn, r ipc.InboxRequest) []core.InboxItem {
	t.Helper()
	var page ipc.InboxPage
	must(t, c.Call(ipc.OpInbox, r, &page))
	return page.Items
}

func conversationsOf(t *testing.T, c *ipc.Conn) []ipc.Conversation {
	t.Helper()
	var page ipc.ConversationPage
	must(t, c.Call(ipc.OpConversations, nil, &page))
	return page.Items
}

func runsOf(t *testing.T, c *ipc.Conn, id string) []core.JobRun {
	t.Helper()
	var page ipc.JobRunPage
	must(t, c.Call(ipc.OpJobRuns, ipc.JobRunsRequest{ID: id}, &page))
	return page.Items
}
