package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/version"
)

// fakeDaemon answers ipc ops on the daemon's socket with handlers, and
// records what it was asked.
type fakeDaemon struct {
	t     *testing.T
	p     paths.Paths
	mu    sync.Mutex
	ops   map[string]func(json.RawMessage) (any, error)
	calls []string // "op data"
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmdf") // short: sockets have a length limit
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	t.Setenv("ACMD_CONFIG_DIR", p.ConfigDir)
	t.Setenv("ACMD_NO_AUTO_INIT", "1")
	orig := spawnDaemon
	spawnDaemon = func(paths.Paths) error { return errors.New("tests start the daemon themselves") }
	t.Cleanup(func() { spawnDaemon = orig })
	must(t, os.MkdirAll(p.RuntimeDir, 0o700))
	ln, err := net.Listen("unix", p.Socket())
	must(t, err)
	t.Cleanup(func() { ln.Close() })
	f := &fakeDaemon{t: t, p: p, ops: map[string]func(json.RawMessage) (any, error){}}
	f.handle(ipc.OpHello, func(json.RawMessage) (any, error) {
		return ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}, nil
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeDaemon) handle(op string, fn func(json.RawMessage) (any, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops[op] = fn
}

// reply answers op with v always.
func (f *fakeDaemon) reply(op string, v any) {
	f.handle(op, func(json.RawMessage) (any, error) { return v, nil })
}

func (f *fakeDaemon) fail(op, msg string) {
	f.handle(op, func(json.RawMessage) (any, error) { return nil, errors.New(msg) })
}

func (f *fakeDaemon) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		var req ipc.Request
		if ipc.ReadJSON(r, &req) != nil {
			return
		}
		f.mu.Lock()
		fn := f.ops[req.Op]
		if req.Op != ipc.OpHello {
			f.calls = append(f.calls, strings.TrimSpace(req.Op+" "+string(req.Data)))
		}
		f.mu.Unlock()
		resp := ipc.Response{OK: true}
		if fn == nil {
			resp = ipc.Response{Error: "unknown op " + req.Op}
		} else if v, err := fn(req.Data); err != nil {
			resp = ipc.Response{Error: err.Error()}
		} else if v != nil {
			b, _ := json.Marshal(v)
			resp.Data = b
		}
		if ipc.WriteJSON(c, resp) != nil {
			return
		}
	}
}

func (f *fakeDaemon) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// calledWith says whether op was called with data containing each of want.
func (f *fakeDaemon) calledWith(op string, want ...string) bool {
	for _, c := range f.called() {
		if c != op && !strings.HasPrefix(c, op+" ") {
			continue
		}
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(c, w)
		}
		if ok {
			return true
		}
	}
	return false
}

func errContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("error %v, want one with %q", err, want)
	}
}

var sampleBrowse = ipc.RemoteBrowse{Host: "studio", Sessions: []ipc.RemoteBrowseSession{
	{Session: api.Session{ID: "r1", Name: "auth", Harness: "claude", Dir: "/w/api", Status: "busy", Unseen: true}, Added: true, LocalID: "L1"},
	{Session: api.Session{ID: "r2", Name: "docs", Harness: "terminal", Dir: "/w/docs", Status: "waiting"}},
	{Session: api.Session{ID: "r3", Name: "docs-site", Harness: "claude", Dir: "/w/site", Status: "done"}},
	{Session: api.Session{ID: "r4", Harness: "claude", Dir: "/w/x", Status: "done"}},
}}

func TestResolveRemoteSession(t *testing.T) {
	for _, tc := range []struct{ sel, want, err string }{
		{sel: "r2", want: "r2"},
		{sel: "1", want: "r1"},
		{sel: "#3", want: "r3"},
		{sel: "AUTH", want: "r1"},
		{sel: "docs", want: "r2"}, // an exact name beats a prefix
		{sel: "docs-", want: "r3"},
		{sel: "au", want: "r1"},
		{sel: "r", err: "matches more than one"},
		{sel: "do", err: "matches more than one Remote session on studio: docs (r2), docs-site (r3)"},
		{sel: "9", err: "no Remote session number 9"},
		{sel: "#0", err: "no Remote session number 0"},
		{sel: "nope", err: `studio has no Remote session "nope" (see acmd remote browse studio)`},
		{sel: " ", err: "has no Remote session"},
	} {
		s, err := resolveRemoteSession(sampleBrowse, tc.sel)
		if tc.err != "" {
			errContains(t, err, tc.err)
			continue
		}
		if err != nil || s.ID != tc.want {
			t.Errorf("%q: %s, %v; want %s", tc.sel, s.ID, err, tc.want)
		}
	}
}

func TestHostProblemsAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		h    ipc.RemoteHost
		code int
		msg  string
	}{
		{ipc.RemoteHost{Name: "a", Address: "a:1", State: ipc.HostUnreachable, Error: "connection refused"}, 3, "a (a:1) can't be reached: connection refused"},
		{ipc.RemoteHost{Name: "a", Address: "a:1", State: ipc.HostConnecting}, 3, "a (a:1) can't be reached"},
		{ipc.RemoteHost{Name: "a", State: ipc.HostPending, OurFingerprint: "SHA256:me"}, 4, "waiting for approval: on a, approve this machine with acmd remote requests (your fingerprint is SHA256:me)"},
		{ipc.RemoteHost{Name: "a", State: ipc.HostUnauthorized, OurFingerprint: "SHA256:me"}, 5, "on a, run acmd remote authorize SHA256:me"},
		{ipc.RemoteHost{Name: "a", State: ipc.HostFingerprint, Fingerprint: "SHA256:p", Seen: "SHA256:s"}, 6, "it shows SHA256:s, but you pinned SHA256:p. If its identity changed on purpose, run acmd remote edit a --fingerprint SHA256:s"},
		{ipc.RemoteHost{Name: "a", State: ipc.HostVersion, Error: "api 2"}, 7, "a speaks another protocol version: api 2"},
		{ipc.RemoteHost{Name: "a", State: ipc.HostVersion}, 7, "update acmd on both machines"},
	} {
		err := hostProblem(tc.h)
		if ExitCode(err) != tc.code {
			t.Errorf("%s: code %d, want %d", tc.h.State, ExitCode(err), tc.code)
		}
		errContains(t, err, tc.msg)
	}
	for _, s := range []string{ipc.HostConnected, ipc.HostIdle, ipc.HostError} {
		if err := hostProblem(ipc.RemoteHost{State: s}); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	if ExitCode(nil) != 0 || ExitCode(errors.New("x")) != 1 || !errors.Is(&ExitError{Code: 4, Err: ErrSilent}, ErrSilent) {
		t.Error("ExitCode")
	}
}

func TestRemoteHelpListsExitCodes(t *testing.T) {
	out := mustRun(t, "remote", "--help")
	for _, want := range []string{"Remote hosts", "acmd remote add", "4  waiting for approval", "6  fingerprint mismatch",
		"add", "browse", "open", "attach", "drop", "retry"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	for _, c := range remoteClientCmds() {
		if !strings.Contains(c.Short+c.Long, "Remote host") {
			t.Errorf("%s's help doesn't say Remote host: %q", c.Name(), c.Short)
		}
	}
}

func TestRemoteAddEditRemove(t *testing.T) {
	f := newFakeDaemon(t)
	f.handle(ipc.OpRemoteHostAdd, func(b json.RawMessage) (any, error) {
		var e ipc.RemoteHostEdit
		_ = json.Unmarshal(b, &e)
		return ipc.RemoteHost{Name: e.Name, Address: e.Address, Fingerprint: cmpOr(e.Fingerprint, "SHA256:fromcert"), OurFingerprint: "SHA256:me"}, nil
	})
	if _, err := acmd(t, "remote", "add", "studio", "studio:7433"); err == nil {
		t.Error("add without a fingerprint or cert")
	}
	if _, err := acmd(t, "remote", "add", "studio", "studio:7433", "--fingerprint", "SHA256:x", "--cert", "c.pem"); err == nil {
		t.Error("add with both")
	}
	out := mustRun(t, "remote", "add", "studio", "studio:7433", "--fingerprint", "SHA256:host")
	for _, want := range []string{`Added the Remote host "studio" (studio:7433), pinned to SHA256:host.`,
		"On studio, authorize this machine: acmd remote authorize SHA256:me", "acmd remote browse studio"} {
		if !strings.Contains(out, want) {
			t.Errorf("add lacks %q:\n%s", want, out)
		}
	}
	mustRun(t, "remote", "add", "lab", "lab:7433", "--cert", "rel/host.pem")
	wd, _ := os.Getwd()
	if !f.calledWith(ipc.OpRemoteHostAdd, `"cert":"`+filepath.Join(wd, "rel/host.pem")+`"`) {
		t.Errorf("the cert path isn't absolute: %v", f.called())
	}

	f.handle(ipc.OpRemoteHostEdit, func(b json.RawMessage) (any, error) {
		var e ipc.RemoteHostEdit
		_ = json.Unmarshal(b, &e)
		return ipc.RemoteHost{Name: cmpOr(e.Name, e.Host), Address: cmpOr(e.Address, "old:1"), Fingerprint: "SHA256:host"}, nil
	})
	if _, err := acmd(t, "remote", "edit", "studio"); err == nil {
		t.Error("edit with nothing to change")
	}
	if _, err := acmd(t, "remote", "edit", "studio", "--fingerprint", "SHA256:x", "--cert", "c.pem"); err == nil {
		t.Error("edit with both")
	}
	out = mustRun(t, "remote", "edit", "studio", "--name", "mini", "--address", "mini:7433")
	if !strings.Contains(out, `Changed the Remote host "mini": mini:7433, pinned to SHA256:host.`) ||
		!f.calledWith(ipc.OpRemoteHostEdit, `"host":"studio"`, `"name":"mini"`) {
		t.Errorf("edit:\n%s\n%v", out, f.called())
	}
	mustRun(t, "remote", "edit", "mini", "--cert", "h.pem")
	if !f.calledWith(ipc.OpRemoteHostEdit, `"cert":"`+filepath.Join(wd, "h.pem")+`"`) {
		t.Errorf("edit --cert: %v", f.called())
	}

	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "mini", Sessions: 2}, {Name: "lab"}})
	f.reply(ipc.OpRemoteHostRemove, nil)
	if _, err := acmd(t, "remote", "remove", "nope", "--yes"); err == nil {
		t.Error("remove an unknown host")
	}
	_, err := acmd(t, "remote", "remove", "mini")
	errContains(t, err, "pass --yes") // no terminal to ask on
	if f.calledWith(ipc.OpRemoteHostRemove) {
		t.Error("removed without confirmation")
	}
	out = mustRun(t, "remote", "remove", "mini", "--yes")
	if !strings.Contains(out, `Removed the Remote host "mini". Its sessions on mini weren't touched.`) ||
		!f.calledWith(ipc.OpRemoteHostRemove, `"host":"mini"`) {
		t.Errorf("remove:\n%s", out)
	}
	f.fail(ipc.OpRemoteHostRemove, "config is read-only")
	_, err = acmd(t, "remote", "remove", "lab", "--yes")
	errContains(t, err, "config is read-only")
	f.fail(ipc.OpRemoteHostAdd, "there's already a Remote host named \"lab\"")
	_, err = acmd(t, "remote", "add", "lab", "lab:1", "--fingerprint", "SHA256:x")
	errContains(t, err, "already a Remote host")
	f.fail(ipc.OpRemoteHostEdit, "no such host")
	_, err = acmd(t, "remote", "edit", "x", "--name", "y")
	errContains(t, err, "no such host")
}

func TestRemoteList(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpRemoteHosts, nil)
	if out := mustRun(t, "remote", "list"); !strings.Contains(out, "No Remote hosts. Add one with: acmd remote add") {
		t.Errorf("empty list:\n%s", out)
	}
	if out := mustRun(t, "remote", "list", "--json"); strings.TrimSpace(out) != "[]" {
		t.Errorf("empty --json: %q", out)
	}
	hosts := []ipc.RemoteHost{
		{Name: "studio", Address: "studio:7433", State: ipc.HostConnected, HostName: "Studio", HostVersion: "1.2.0", Sessions: 2},
		{Name: "lab", Address: "lab:7433", State: ipc.HostPending, OurFingerprint: "SHA256:me"},
		{Name: "moved", Address: "m:7433", State: ipc.HostFingerprint, Seen: "SHA256:seen"},
		{Name: "away", Address: "a:7433", State: ipc.HostUnreachable, Error: "connection refused"},
		{Name: "quiet", Address: "q:7433", State: ipc.HostIdle},
		{Name: "fresh", Address: "f:7433", State: ipc.HostConnected},
	}
	f.reply(ipc.OpRemoteHosts, hosts)
	out := mustRun(t, "remote", "list")
	for _, want := range []string{"NAME    ADDRESS      STATE        SESSIONS  NOTE",
		"studio  studio:7433  connected    2         Studio, acmd 1.2.0",
		"lab     lab:7433     pending      0         waiting for approval — your fingerprint is SHA256:me",
		"moved   m:7433       fingerprint  0         fingerprint mismatch: it shows SHA256:seen",
		"away    a:7433       unreachable  0         connection refused",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var got []ipc.RemoteHost
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "list", "--json")), &got))
	if len(got) != len(hosts) || got[1].OurFingerprint != "SHA256:me" || got[0].Sessions != 2 {
		t.Errorf("list --json = %+v", got)
	}
}

func TestRemoteRetry(t *testing.T) {
	f := newFakeDaemon(t)
	orig := retryWait
	retryWait = 2 * time.Second
	t.Cleanup(func() { retryWait = orig })

	f.reply(ipc.OpRemoteHostRetry, ipc.RemoteHost{Name: "studio", State: ipc.HostConnecting})
	var mu sync.Mutex
	n := 0
	f.handle(ipc.OpRemoteHosts, func(json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n < 3 {
			return []ipc.RemoteHost{{Name: "studio", State: ipc.HostConnecting}}, nil
		}
		return []ipc.RemoteHost{{Name: "studio", State: ipc.HostConnected, HostVersion: "1.0"}}, nil
	})
	if out := mustRun(t, "remote", "retry", "studio"); !strings.Contains(out, "Connected to studio (studio, acmd 1.0).") {
		t.Errorf("retry:\n%s", out)
	}
	// A failure before the retry, and the same one after this try (Since moved).
	before, after := time.Unix(1000, 0), time.Unix(2000, 0)
	for state, code := range map[string]int{ipc.HostPending: 4, ipc.HostUnauthorized: 5, ipc.HostFingerprint: 6, ipc.HostVersion: 7,
		ipc.HostUnreachable: 3, ipc.HostError: 1} {
		f.reply(ipc.OpRemoteHostRetry, ipc.RemoteHost{Name: "studio", State: state, Since: before})
		f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "studio", State: state, Since: after, Error: "the host's cert: missing"}})
		if _, err := acmd(t, "remote", "retry", "studio"); ExitCode(err) != code {
			t.Errorf("retry %s: %v (code %d), want %d", state, err, ExitCode(err), code)
		}
	}
	f.reply(ipc.OpRemoteHostRetry, ipc.RemoteHost{Name: "studio", State: ipc.HostError, Since: before})
	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "studio", State: ipc.HostError, Since: after, Error: "the host's cert: missing"}})
	_, err := acmd(t, "remote", "retry", "studio")
	errContains(t, err, "studio: the host's cert: missing")
	// A failure that never tries again in the wait: it's what it was.
	retryWait = 150 * time.Millisecond
	f.reply(ipc.OpRemoteHostRetry, ipc.RemoteHost{Name: "studio", State: ipc.HostPending, Since: before})
	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "studio", State: ipc.HostPending, Since: before}})
	if _, err := acmd(t, "remote", "retry", "studio"); ExitCode(err) != 4 {
		t.Errorf("retry with no new try: %v", err)
	}
	// Still connecting when the wait is over.
	retryWait = 150 * time.Millisecond
	f.reply(ipc.OpRemoteHostRetry, ipc.RemoteHost{Name: "studio", State: ipc.HostConnecting})
	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "studio", State: ipc.HostConnecting}})
	if _, err := acmd(t, "remote", "retry", "studio"); ExitCode(err) != 3 {
		t.Errorf("retry still connecting: %v", err)
	}
	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{})
	_, err = acmd(t, "remote", "retry", "studio")
	errContains(t, err, `no Remote host named "studio"`)
	f.fail(ipc.OpRemoteHostRetry, `there's no Remote host named "x"`)
	_, err = acmd(t, "remote", "retry", "x")
	errContains(t, err, "no Remote host")
}

func TestRemoteBrowse(t *testing.T) {
	f := newFakeDaemon(t)
	now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
	b := sampleBrowse
	b.Sessions = append([]ipc.RemoteBrowseSession(nil), b.Sessions...)
	b.Sessions[0].LastActivity = now().Add(-5 * time.Minute)
	f.reply(ipc.OpRemoteBrowse, b)
	out := mustRun(t, "remote", "browse", "studio")
	for _, want := range []string{"#  STATUS   NAME       HARNESS   DIR   ADDED  LAST  ID",
		"1  busy•    auth       claude    api   added  5m    r1",
		"2  waiting  docs       terminal  docs  -      -     r2",
		"4  done     (unnamed)  claude    x     -      -     r4"} {
		if !strings.Contains(out, want) {
			t.Errorf("browse lacks %q:\n%s", want, out)
		}
	}
	var got browseJSON
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "browse", "studio", "--json")), &got))
	if got.Host != "studio" || len(got.Sessions) != 4 || got.Sessions[0].Number != 1 || !got.Sessions[0].Added ||
		got.Sessions[0].LocalID != "L1" || got.Sessions[1].Name != "docs" {
		t.Errorf("browse --json = %+v", got)
	}
	f.reply(ipc.OpRemoteBrowse, ipc.RemoteBrowse{Host: "studio", Sessions: []ipc.RemoteBrowseSession{}})
	if out := mustRun(t, "remote", "browse", "studio"); !strings.Contains(out, "studio has no Remote sessions. On studio, make one with acmd remote on <session>.") {
		t.Errorf("empty browse:\n%s", out)
	}
	if out := mustRun(t, "remote", "browse", "studio", "--json"); !strings.Contains(out, `"sessions": []`) {
		t.Errorf("empty browse --json:\n%s", out)
	}
}

// TestRemoteExitCodes: a call that fails because the host can't be used
// exits with that state's code; any other failure exits 1.
func TestRemoteExitCodes(t *testing.T) {
	f := newFakeDaemon(t)
	f.fail(ipc.OpRemoteBrowse, "studio isn't connected")
	f.fail(ipc.OpRemoteNew, "studio isn't connected")
	for state, code := range map[string]int{ipc.HostUnreachable: 3, ipc.HostPending: 4, ipc.HostUnauthorized: 5,
		ipc.HostFingerprint: 6, ipc.HostVersion: 7, ipc.HostConnected: 1, ipc.HostError: 1} {
		f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "studio", State: state}})
		for _, args := range [][]string{{"remote", "browse", "studio"}, {"remote", "open", "studio", "1"},
			{"remote", "drop", "studio", "1"}, {"new", "--on", "studio"}} {
			if _, err := acmd(t, args...); err == nil || ExitCode(err) != code {
				t.Errorf("%s with %s: %v (code %d), want %d", strings.Join(args, " "), state, err, ExitCode(err), code)
			}
		}
	}
	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{})
	_, err := acmd(t, "remote", "browse", "studio")
	errContains(t, err, "studio isn't connected")
	if ExitCode(err) != 1 {
		t.Errorf("unknown host: code %d", ExitCode(err))
	}
	f.fail(ipc.OpRemoteHosts, "boom")
	if _, err := acmd(t, "remote", "browse", "studio"); ExitCode(err) != 1 {
		t.Errorf("hosts unavailable: code %d", ExitCode(err))
	}
}

// fakeAttach records attaches instead of taking over the terminal.
func fakeAttach(t *testing.T, terminal bool, err error) *[]string {
	t.Helper()
	var sockets []string
	origAttach, origTerm := attachTerminal, stdinTerminal
	attachTerminal = func(_ context.Context, socket string) error {
		sockets = append(sockets, socket)
		return err
	}
	stdinTerminal = func(*cobra.Command) bool { return terminal }
	t.Cleanup(func() { attachTerminal, stdinTerminal = origAttach, origTerm })
	return &sockets
}

func TestRemoteOpen(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpRemoteBrowse, sampleBrowse)
	f.handle(ipc.OpRemoteSessionAdd, func(b json.RawMessage) (any, error) {
		var r ipc.RemoteHostRef
		_ = json.Unmarshal(b, &r)
		return core.Session{ID: "L-" + r.ID, Host: r.Host, RemoteID: r.ID}, nil
	})
	sockets := fakeAttach(t, true, nil)

	// Added already: attaches to the local session, without adding it again.
	mustRun(t, "remote", "open", "studio", "auth")
	if f.calledWith(ipc.OpRemoteSessionAdd) || len(*sockets) != 1 || (*sockets)[0] != f.p.HostSocket("L1") {
		t.Errorf("open an added session: %v, %v", *sockets, f.called())
	}
	// Not added: added, then attached.
	mustRun(t, "remote", "open", "studio", "#2")
	if !f.calledWith(ipc.OpRemoteSessionAdd, `"host":"studio"`, `"id":"r2"`) || (*sockets)[1] != f.p.HostSocket("L-r2") {
		t.Errorf("open: %v, %v", *sockets, f.called())
	}
	if f.calledWith(ipc.OpRemoteSessionRemove) {
		t.Error("open removed the session")
	}

	// Without a terminal it's only added.
	fakeAttach(t, false, nil)
	out := mustRun(t, "remote", "open", "studio", "r3")
	if !strings.Contains(out, `Added "docs-site" on studio (L-r3). Attach to it from a terminal with acmd remote open studio r3.`) {
		t.Errorf("open without a terminal:\n%s", out)
	}
	_, err := acmd(t, "remote", "open", "studio", "nope")
	errContains(t, err, `studio has no Remote session "nope"`)
	f.fail(ipc.OpRemoteSessionAdd, "studio has no Remote session \"r4\"")
	f.reply(ipc.OpRemoteHosts, []ipc.RemoteHost{{Name: "studio", State: ipc.HostConnected}})
	_, err = acmd(t, "remote", "open", "studio", "4")
	errContains(t, err, `no Remote session "r4"`)
}

func TestRemoteAttach(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpRemoteBrowse, sampleBrowse)
	f.handle(ipc.OpRemoteSessionAdd, func(b json.RawMessage) (any, error) {
		var r ipc.RemoteHostRef
		_ = json.Unmarshal(b, &r)
		return core.Session{ID: "L-" + r.ID}, nil
	})
	f.reply(ipc.OpRemoteSessionRemove, nil)

	fakeAttach(t, false, nil)
	_, err := acmd(t, "remote", "attach", "studio", "docs")
	errContains(t, err, "needs a terminal")

	sockets := fakeAttach(t, true, nil)
	// Not added: added for the attach, removed after it.
	mustRun(t, "remote", "attach", "studio", "docs")
	if len(*sockets) != 1 || (*sockets)[0] != f.p.HostSocket("L-r2") || !f.calledWith(ipc.OpRemoteSessionRemove, `"id":"L-r2"`) {
		t.Errorf("attach: %v, %v", *sockets, f.called())
	}
	// Added already: it stays added.
	mustRun(t, "remote", "attach", "studio", "auth")
	if f.calledWith(ipc.OpRemoteSessionRemove, `"id":"L1"`) {
		t.Error("attach removed a session that was added before")
	}
	// The attach fails, and removing it fails too: both are said.
	fakeAttach(t, true, errors.New("socket gone"))
	f.fail(ipc.OpRemoteSessionRemove, "store is busy")
	_, err = acmd(t, "remote", "attach", "studio", "docs")
	errContains(t, err, "socket gone")
	errContains(t, err, "still added here: remove it with acmd remote drop L-r2")
	_, err = acmd(t, "remote", "attach", "studio", "zzz")
	errContains(t, err, "no Remote session")
}

func TestRemoteDrop(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpRemoteBrowse, sampleBrowse)
	f.reply(ipc.OpRemoteSessionRemove, nil)
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Sessions: ipc.SessionsOf([]*core.Session{
		{ID: "L1", Name: "auth", Kind: core.KindHosted, Host: "studio", RemoteID: "r1"},
		{ID: "loc", Name: "local", Kind: core.KindHosted},
	})})
	out := mustRun(t, "remote", "drop", "studio", "1")
	if !strings.Contains(out, `Removed "auth" on studio here; it keeps running on the host.`) || !f.calledWith(ipc.OpRemoteSessionRemove, `"id":"L1"`) {
		t.Errorf("drop host session:\n%s", out)
	}
	_, err := acmd(t, "remote", "drop", "studio", "docs")
	errContains(t, err, `"docs" on studio isn't added here`)
	out = mustRun(t, "remote", "drop", "auth")
	if !strings.Contains(out, `Removed "auth" on studio here`) {
		t.Errorf("drop local selector:\n%s", out)
	}
	_, err = acmd(t, "remote", "drop", "local")
	errContains(t, err, "runs on this machine, not on a Remote host")
	_, err = acmd(t, "remote", "drop", "zzz")
	errContains(t, err, "zzz")
	f.fail(ipc.OpRemoteSessionRemove, "nope")
	_, err = acmd(t, "remote", "drop", "auth")
	errContains(t, err, "nope")
	f.fail(ipc.OpSnapshot, "no snapshot")
	_, err = acmd(t, "remote", "drop", "auth")
	errContains(t, err, "no snapshot")
}

func TestNewOn(t *testing.T) {
	f := newFakeDaemon(t)
	f.handle(ipc.OpRemoteNew, func(b json.RawMessage) (any, error) {
		var r ipc.RemoteNewRequest
		_ = json.Unmarshal(b, &r)
		return core.Session{ID: "01HZZZZZZZZZZZZZZZZZZZZZZZ", Name: r.Name, Cwd: "/home/me/" + strings.TrimPrefix(r.Dir, "~/"), Host: r.Host}, nil
	})
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Groups: ipc.GroupsOf([]*core.Group{{ID: "g1", Name: "Work"}})})
	f.reply(ipc.OpMutate, core.Group{ID: "g2", Name: "New"})

	out := mustRun(t, "new", "--on", "studio", "--dir", "~/api", "--harness", "terminal", "--name", "api", "--prompt", "hi")
	if !strings.Contains(out, `Started "api" on studio in /home/me/api`) ||
		!f.calledWith(ipc.OpRemoteNew, `"host":"studio"`, `"dir":"~/api"`, `"harness":"terminal"`, `"name":"api"`, `"prompt":"hi"`) {
		t.Errorf("new --on:\n%s\n%v", out, f.called())
	}
	mustRun(t, "new", "--on", "studio", "--group", "work", "--", "fix", "it")
	if !f.calledWith(ipc.OpRemoteNew, `"dir":"~"`, `"prompt":"fix it"`, `"group_id":"g1"`) {
		t.Errorf("new --on with a group: %v", f.called())
	}
	mustRun(t, "new", "--on", "studio", "--group", "New")
	if !f.calledWith(ipc.OpRemoteNew, `"group_id":"g2"`) {
		t.Errorf("new --on created no group: %v", f.called())
	}
	_, err := acmd(t, "new", "--on", "studio", "--group", "Nope", "--no-create")
	errContains(t, err, `no group named "Nope"`)
	for _, args := range [][]string{
		{"new", "--on", "studio", "--cwd", "/tmp"},
		{"new", "--on", "studio", "--remote"},
		{"new", "--on", "studio", "--description", "x"},
	} {
		_, err := acmd(t, args...)
		errContains(t, err, "isn't for a session on a Remote host")
	}
	_, err = acmd(t, "new", "--dir", "~/api")
	errContains(t, err, "--dir is the folder on a Remote host")
	_, err = acmd(t, "new", "--on", "studio", "--prompt", "a", "--", "b")
	errContains(t, err, "not both")
	_, err = acmd(t, "new", "--on", "studio", "--harness", "copilot")
	errContains(t, err, "claude, antigravity or terminal")
	f.fail(ipc.OpSnapshot, "no snapshot")
	_, err = acmd(t, "new", "--on", "studio", "--group", "x")
	errContains(t, err, "no snapshot")
}

func TestRemoteCompletion(t *testing.T) {
	f := newFakeDaemon(t)
	must(t, os.MkdirAll(f.p.ConfigDir, 0o700))
	must(t, os.WriteFile(f.p.Config(), []byte("[[remote.hosts]]\nname = \"studio\"\naddress = \"s:7433\"\nfingerprint = \"SHA256:x\"\n\n"+
		"[[remote.hosts]]\nname = \"lab\"\naddress = \"l:7433\"\nfingerprint = \"SHA256:y\"\n"), 0o600))
	f.reply(ipc.OpRemoteBrowse, ipc.RemoteBrowse{Host: "studio", Sessions: []ipc.RemoteBrowseSession{
		{Session: api.Session{ID: "r1", Name: "auth"}}, {Session: api.Session{ID: "r2", Name: "two words"}}, {Session: api.Session{ID: "r3"}}}})
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Sessions: ipc.SessionsOf([]*core.Session{
		{ID: "L1", Name: "auth", Host: "studio"}, {ID: "loc", Name: "local"}, {ID: "L2", Host: "lab", ArchivedAt: time.Now()}})})

	complete := func(args ...string) string {
		t.Helper()
		return mustRun(t, append([]string{cobra.ShellCompRequestCmd}, args...)...)
	}
	for _, tc := range []struct {
		args      []string
		want, not []string
	}{
		{[]string{"remote", "browse", ""}, []string{"studio", "lab"}, nil},
		{[]string{"remote", "browse", "studio", ""}, nil, []string{"lab"}},
		{[]string{"remote", "edit", ""}, []string{"studio", "lab"}, nil},
		{[]string{"remote", "open", ""}, []string{"studio", "lab"}, nil},
		{[]string{"remote", "open", "studio", ""}, []string{"auth\tr1", "r1\tauth", "r2\ttwo words", "r3\t(unnamed)"}, []string{"two words\t"}},
		{[]string{"remote", "attach", "studio", "a", ""}, nil, []string{"auth"}},
		{[]string{"remote", "drop", ""}, []string{"studio", "lab", "L1\tauth on studio"}, []string{"loc", "L2"}},
		{[]string{"remote", "drop", "studio", ""}, []string{"r1\tauth"}, nil},
		{[]string{"new", "--on", ""}, []string{"studio", "lab"}, nil},
	} {
		out := complete(tc.args...)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("complete %q lacks %q:\n%s", tc.args, w, out)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(out, n) {
				t.Errorf("complete %q has %q:\n%s", tc.args, n, out)
			}
		}
	}
	// No daemon: no sessions, and nothing is started.
	f.fail(ipc.OpRemoteBrowse, "down")
	f.fail(ipc.OpSnapshot, "down")
	if out := complete("remote", "open", "studio", ""); strings.Contains(out, "r1") {
		t.Errorf("completion without a browse:\n%s", out)
	}
	if out := complete("remote", "drop", ""); strings.Contains(out, "L1") {
		t.Errorf("completion without a snapshot:\n%s", out)
	}
}

func TestCompletionWithoutADaemon(t *testing.T) {
	isolate(t)
	if got := hostSessionNames(context.Background(), "studio"); got != nil {
		t.Errorf("sessions without a daemon: %v", got)
	}
	if got := addedSessionNames(context.Background()); got != nil {
		t.Errorf("added sessions without a daemon: %v", got)
	}
	if got := hostNames(); got != nil {
		t.Errorf("hosts without a config: %v", got)
	}
}

func TestListShowsTheHost(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
	g := &core.Group{ID: "default", Name: "Default"}
	rows := []listRow{
		{Number: 1, Group: g, Session: &core.Session{ID: "a", Name: "auth", Kind: core.KindHosted, Host: "studio", RemoteID: "r1", Status: core.StatusBusy}},
		{Number: 2, Group: g, Session: &core.Session{ID: "b", Name: "old", Kind: core.KindHosted, Host: "lab", RemoteID: "r9", HostGone: true, Status: core.StatusPaused}},
		{Number: 3, Group: g, Session: &core.Session{ID: "c", Name: "here", Kind: core.KindHosted, Status: core.StatusDone}},
	}
	var b strings.Builder
	must(t, writeListTable(&b, rows))
	for _, want := range []string{"↗ studio", "↗ lab (gone)", "acmd"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	must(t, writeListJSON(&b, rows))
	var got []jsonRow
	must(t, json.Unmarshal([]byte(b.String()), &got))
	if got[0].Host != "studio" || got[0].RemoteID != "r1" || got[0].Where != "↗ studio" || !got[1].HostGone || got[2].Host != "" {
		t.Errorf("list --json = %+v", got)
	}
	if n := strings.Count(b.String(), `"host":`); n != 2 {
		t.Errorf("host in %d rows, want the 2 on hosts:\n%s", n, b.String())
	}
}

func TestBeforeDetach(t *testing.T) {
	for _, tc := range []struct {
		in       string
		detach   byte
		want     string
		detached bool
	}{
		{"abc", detachKey, "abc", false},
		{"ab\x1dcd", detachKey, "ab", true},
		{"\x1d", detachKey, "", true},
		{"ab\x1dcd", 0, "ab\x1dcd", false},
	} {
		got, d := beforeDetach([]byte(tc.in), tc.detach)
		if string(got) != tc.want || d != tc.detached {
			t.Errorf("%q: %q %v", tc.in, got, d)
		}
	}
}

// TestRemoteHostsEndToEnd: a real daemon is its own Remote host, over
// loopback, so each state's exit code comes from a real connection.
func TestRemoteHostsEndToEnd(t *testing.T) {
	p, port := remoteEnv(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	mustRun(t, "remote", "hosting", "on")
	me, err := identity.Ensure(p.RemoteIdentityDir())
	must(t, err)

	out := mustRun(t, "remote", "add", "self", addr, "--fingerprint", me.Fingerprint())
	if !strings.Contains(out, "acmd remote authorize "+me.Fingerprint()) {
		t.Errorf("add:\n%s", out)
	}
	// Not authorized yet: waiting for approval.
	_, err = acmd(t, "remote", "browse", "self")
	if ExitCode(err) != exitPending {
		t.Errorf("browse before approval: %v (code %d)", err, ExitCode(err))
	}
	if out := mustRun(t, "remote", "list"); !strings.Contains(out, "waiting for approval — your fingerprint is "+me.Fingerprint()) {
		t.Errorf("list while pending:\n%s", out)
	}
	mustRun(t, "remote", "authorize", me.Fingerprint(), "--name", "me")
	if out := mustRun(t, "remote", "retry", "self"); !strings.Contains(out, "Connected to self") {
		t.Errorf("retry after approval:\n%s", out)
	}
	if out := mustRun(t, "remote", "browse", "self"); !strings.Contains(out, "self has no Remote sessions") {
		t.Errorf("browse:\n%s", out)
	}

	// Pinned to another fingerprint.
	other, err := identity.Ensure(filepath.Join(p.StateDir, "other"))
	must(t, err)
	mustRun(t, "remote", "add", "wrong", addr, "--fingerprint", other.Fingerprint())
	_, err = acmd(t, "remote", "browse", "wrong")
	if ExitCode(err) != exitFingerprint || !strings.Contains(err.Error(), "it shows "+me.Fingerprint()) {
		t.Errorf("browse a mismatched host: %v (code %d)", err, ExitCode(err))
	}
	_, err = acmd(t, "new", "--on", "wrong")
	if ExitCode(err) != exitFingerprint {
		t.Errorf("new --on a mismatched host: %v (code %d)", err, ExitCode(err))
	}

	// Nothing listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	closed := ln.Addr().String()
	_ = ln.Close()
	mustRun(t, "remote", "add", "gone", closed, "--fingerprint", me.Fingerprint())
	if _, err := acmd(t, "remote", "browse", "gone"); ExitCode(err) != exitUnreachable {
		t.Errorf("browse an unreachable host: %v (code %d)", err, ExitCode(err))
	}

	mustRun(t, "remote", "remove", "gone", "--yes")
	if out := mustRun(t, "remote", "list"); strings.Contains(out, "gone") {
		t.Errorf("list after remove:\n%s", out)
	}
	mustRun(t, "remote", "edit", "wrong", "--fingerprint", me.Fingerprint())
	if out := mustRun(t, "remote", "retry", "wrong"); !strings.Contains(out, "Connected to wrong") {
		t.Errorf("retry after the edit:\n%s", out)
	}

	// Hosting can't listen on a port that's taken, without the fallback.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	defer taken.Close()
	mustRun(t, "remote", "hosting", "off")
	mustRun(t, "config", "set", "remote.host.port_fallback", "false")
	mustRun(t, "config", "set", "remote.host.port", strconv.Itoa(taken.Addr().(*net.TCPAddr).Port))
	if _, err := acmd(t, "remote", "hosting", "on"); ExitCode(err) != exitPortTaken {
		t.Errorf("hosting on a taken port: %v (code %d)", err, ExitCode(err))
	}
}

// --host runs a command's ops on a Remote host, through this machine's daemon
// (remote.call); commands for this machine only refuse it.
func TestHostFlagRunsOpsOnTheHost(t *testing.T) {
	f := newFakeDaemon(t)
	var inner []string
	f.handle(ipc.OpRemoteCall, func(data json.RawMessage) (any, error) {
		var rc ipc.RemoteCall
		if err := json.Unmarshal(data, &rc); err != nil || rc.Host != "studio" {
			return nil, fmt.Errorf("bad call %s", data)
		}
		inner = append(inner, rc.Op+" "+string(rc.Data))
		switch rc.Op {
		case ipc.OpSnapshot:
			return ipc.Snapshot{Sessions: ipc.SessionsOf([]*core.Session{{ID: "h1", Name: "api", Kind: core.KindHosted, Status: core.StatusPaused}})}, nil
		case ipc.OpSessionScreen:
			return ipc.Screen{ID: "h1", Text: "on the host", Lines: 1, Total: 1}, nil
		case ipc.OpNew:
			return ipc.SessionOf(&core.Session{ID: "h2", Name: "new", Cwd: "/home/me"}), nil
		case ipc.OpMutate:
			return ipc.Results{Results: []ipc.Result{{ID: "h1", OK: true}}}, nil
		}
		return nil, &ipc.RemoteError{Msg: "studio: can't", Code: "forbidden"}
	})
	if out := mustRun(t, "--host", "studio", "screen", "1"); out != "on the host\n" {
		t.Errorf("screen = %q", out)
	}
	if out := mustRun(t, "--host", "studio", "new"); !strings.Contains(out, `Started "new"`) || !strings.Contains(inner[len(inner)-1], `"cwd":"~"`) {
		t.Errorf("new: %s %v", out, inner)
	}
	mustRun(t, "--host", "studio", "rename", "1", "--to", "x")
	if !strings.Contains(inner[len(inner)-1], `"kind":"session.rename"`) {
		t.Errorf("rename went as %v", inner)
	}
	_, err := acmd(t, "--host", "studio", "resume", "1")
	errContains(t, err, "studio: can't") // the host's refusal
	_, err = acmd(t, "--host", "studio", "keys")
	errContains(t, err, "acmd keys runs on this machine only")
	_, err = acmd(t, "--host", "studio", "new", "--on", "lab")
	errContains(t, err, "give one")
	t.Setenv("ACMD_HOST", "studio")
	if out := mustRun(t, "screen", "1"); out != "on the host\n" {
		t.Errorf("ACMD_HOST: %q", out)
	}
}
