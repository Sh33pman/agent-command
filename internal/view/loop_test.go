package view

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

var fakeClaude string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeclaude")
	if err != nil {
		panic(err)
	}
	fakeClaude = filepath.Join(dir, "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaude, "../../test/fakeclaude").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeDaemon is an in-memory Client.
type fakeDaemon struct {
	mu       sync.Mutex
	rev      uint64
	ch       chan struct{}
	view     core.View
	gone     bool
	sessions []*core.Session
	resumed  []string
	resumeOK func(id string) // called on Resume
}

func newFakeDaemon(sessions ...*core.Session) *fakeDaemon {
	return &fakeDaemon{ch: make(chan struct{}), view: core.View{ID: "v1"}, sessions: sessions}
}

func (f *fakeDaemon) change(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
	f.rev++
	close(f.ch)
	f.ch = make(chan struct{})
}

func (f *fakeDaemon) Watch(ctx context.Context, req ipc.WatchRequest) (ipc.WatchResponse, error) {
	f.mu.Lock()
	rev, ch := f.rev, f.ch
	f.mu.Unlock()
	if rev == req.Rev && req.WaitMS > 0 {
		select {
		case <-ch:
		case <-time.After(time.Duration(req.WaitMS) * time.Millisecond):
		case <-ctx.Done():
			return ipc.WatchResponse{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.view
	return ipc.WatchResponse{Rev: f.rev, View: &v, Gone: f.gone}, nil
}

func (f *fakeDaemon) Snapshot(context.Context) (ipc.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ipc.Snapshot{Sessions: f.sessions, Groups: []*core.Group{{ID: "default", Name: "Default", IsDefault: true}},
		ViewState: core.ViewState{Mode: "list", Sort: "created"}}, nil
}

func (f *fakeDaemon) Select(_ context.Context, _, id string) error {
	f.change(func() { f.view.Selected = id })
	return nil
}

func (f *fakeDaemon) Close(context.Context, string) error {
	f.change(func() { f.gone = true })
	return nil
}

func (f *fakeDaemon) Resume(_ context.Context, id string) (*core.Session, error) {
	f.mu.Lock()
	f.resumed = append(f.resumed, id)
	fn := f.resumeOK
	f.mu.Unlock()
	if fn == nil {
		return nil, errors.New("transcript gone")
	}
	fn(id)
	return nil, nil
}

func (f *fakeDaemon) selected() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view.Selected
}

// syncBuffer collects output from the loop and attach clients.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	t      *testing.T
	d      *fakeDaemon
	out    *syncBuffer
	keys   *os.File // write end of the pane's stdin
	state  string
	done   chan error
	cancel context.CancelFunc
	news   int
}

func run(t *testing.T, d *fakeDaemon, agents ...string) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{t: t, d: d, out: &syncBuffer{}, state: filepath.Join(dir, "fake.json"), done: make(chan error, 1)}
	var list []map[string]any
	for _, id := range agents {
		list = append(list, map[string]any{"id": id, "sessionId": id + "-x", "kind": "background"})
	}
	b, _ := json.Marshal(map[string]any{"agents": list})
	must(t, os.WriteFile(h.state, b, 0o600))
	r, w, err := os.Pipe()
	must(t, err)
	h.keys = w
	t.Cleanup(func() { r.Close(); w.Close() })

	l := &Loop{
		ViewID: "v1", Client: d, Out: h.out, Keys: &TermKeys{In: r},
		Attach: func(bgID string) *exec.Cmd {
			cmd := exec.Command(fakeClaude, "attach", bgID)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = r, h.out, h.out
			cmd.Env = append(os.Environ(), "FAKECLAUDE_STATE="+h.state)
			return cmd
		},
		New: func(context.Context) error {
			h.news++
			return errors.New("new isn't wired in this test")
		},
		HeartbeatWait: 200 * time.Millisecond,
		QuickExit:     300 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-h.done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrClosed) {
				t.Errorf("loop: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("loop didn't stop")
		}
	})
	return h
}

func (h *harness) waitFor(what string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(h.out.String(), what) {
		if time.Now().After(deadline) {
			h.t.Fatalf("never saw %q in:\n%s", what, h.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *harness) press(s string) {
	h.t.Helper()
	if _, err := h.keys.WriteString(s); err != nil {
		h.t.Fatal(err)
	}
}

func bg(id, bgID, name string, status core.Status) *core.Session {
	return &core.Session{ID: id, BgID: bgID, Name: name, Status: status, Kind: core.KindBackground, GroupID: "default",
		CreatedAt: time.Date(2026, 10, 31, 12, 0, 0, len(id), time.UTC)}
}

func TestSwitchDetachAndPick(t *testing.T) {
	d := newFakeDaemon(bg("s1", "aaaaaaaa", "auth", core.StatusBusy), bg("s2", "bbbbbbbb", "flaky", core.StatusDone))
	d.view.Selected = "s1"
	h := run(t, d, "aaaaaaaa", "bbbbbbbb")
	h.waitFor("ATTACHED aaaaaaaa")

	// The view switches: the old client is ended, the new one attached.
	_ = d.Select(context.Background(), "v1", "s2")
	h.waitFor("ATTACHED bbbbbbbb")

	// The user detaches (even right away: a clean exit): the placeholder lists sessions.
	h.press("detach\n")
	h.waitFor("No session shown in this pane.")
	h.waitFor("1  busy     auth")
	h.waitFor("2  done     flaky")
	if d.selected() != "" {
		t.Errorf("detach left %q selected", d.selected())
	}

	// A key that does nothing, then n (reports its error), then 1.
	h.press("x")
	h.press("n")
	h.waitFor("new isn't wired in this test")
	h.press("1")
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(h.out.String(), "ATTACHED aaaaaaaa") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("1 didn't attach session 1:\n%s", h.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.news != 1 {
		t.Errorf("n pressed once, New called %d times", h.news)
	}
}

func TestPlaceholderRedrawsAndCloses(t *testing.T) {
	d := newFakeDaemon()
	h := run(t, d)
	h.waitFor("No background sessions yet.")
	d.change(func() { d.sessions = append(d.sessions, bg("s1", "aaaaaaaa", "", core.StatusStarting)) })
	h.waitFor("1  starting session aaaaaaaa")
	h.press("q")
	select {
	case err := <-h.done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("q: %v", err)
		}
		h.done <- err // for the cleanup
	case <-time.After(5 * time.Second):
		t.Fatal("q didn't close the view")
	}
	if !d.gone {
		t.Error("q didn't close the view in the daemon")
	}
}

func TestFailedAttachResumesOnce(t *testing.T) {
	d := newFakeDaemon(bg("s1", "cccccccc", "old", core.StatusPaused))
	d.view.Selected = "s1"
	h := run(t, d) // claude doesn't know cccccccc: attach fails at once
	d.mu.Lock()
	d.resumeOK = func(string) {}
	d.mu.Unlock()
	h.waitFor(`Couldn't show "old" (claude attach cccccccc ended at once).`)
	if len(d.resumed) != 1 {
		t.Errorf("resumed %d times", len(d.resumed))
	}

	// Resume itself failing is reported too.
	d.mu.Lock()
	d.resumeOK = nil
	d.mu.Unlock()
	_ = d.Select(context.Background(), "v1", "s1")
	h.waitFor(`Couldn't resume "old": transcript gone`)

	// A running session whose attach fails isn't resumed.
	d.change(func() { d.sessions = append(d.sessions, bg("s2", "dddddddd", "", core.StatusBusy)) })
	_ = d.Select(context.Background(), "v1", "s2")
	h.waitFor(`Couldn't show "session dddddddd"`)
	if len(d.resumed) != 2 {
		t.Errorf("resumed %d times, want 2", len(d.resumed))
	}
	// An unknown session.
	_ = d.Select(context.Background(), "v1", "gone")
	h.waitFor("that session no longer exists")
}

func TestViewClosedWhileAttached(t *testing.T) {
	d := newFakeDaemon(bg("s1", "aaaaaaaa", "a", core.StatusBusy))
	d.view.Selected = "s1"
	h := run(t, d, "aaaaaaaa")
	h.waitFor("ATTACHED aaaaaaaa")
	d.change(func() { d.gone = true })
	select {
	case err := <-h.done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("closed: %v", err)
		}
		h.done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("loop kept running after its view was closed")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
