package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/term"
)

var t0 = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

func remoteSession(id, name string) core.Session {
	return core.Session{ID: id, Name: name, Cwd: "/w/" + name, HarnessKind: core.HarnessTerminal, Status: core.StatusRunning,
		Remote: true, CreatedAt: t0, LastEventAt: t0.Add(time.Hour)}
}

func TestKnownHosts(t *testing.T) {
	l := newLaptop(t)
	dir := t.TempDir()
	hostID, err := identity.Ensure(filepath.Join(dir, "studio"))
	must(t, err)
	fp := hostID.Fingerprint()
	certFile, _ := identity.Files(filepath.Join(dir, "studio"))

	// Checks.
	for _, tc := range []struct {
		e    ipc.RemoteHostEdit
		want string
	}{
		{ipc.RemoteHostEdit{Address: "a:1", Fingerprint: fp}, "needs a name"},
		{ipc.RemoteHostEdit{Name: "x", Address: "nowhere", Fingerprint: fp}, "host:port"},
		{ipc.RemoteHostEdit{Name: "x", Address: ":7433", Fingerprint: fp}, "host:port"},
		{ipc.RemoteHostEdit{Name: "x", Address: "a:99999", Fingerprint: fp}, "host:port"},
		{ipc.RemoteHostEdit{Name: "x", Address: "a:1"}, "fingerprint, or its cert"},
		{ipc.RemoteHostEdit{Name: "x", Address: "a:1", Fingerprint: "SHA256:nope"}, "fingerprint"},
		{ipc.RemoteHostEdit{Name: "x", Address: "a:1", Cert: filepath.Join(dir, "missing.pem")}, "the host's cert"},
		{ipc.RemoteHostEdit{Name: "x", Address: "a:1", Cert: certFile, Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, "the cert's fingerprint is"},
	} {
		if _, err := l.c.AddHost(tc.e); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("add %+v: %v, want %q", tc.e, err, tc.want)
		}
	}

	// By fingerprint (pasted without its prefix), and by cert file: the
	// cert's fingerprint is pinned, and its path kept.
	st, err := l.c.AddHost(ipc.RemoteHostEdit{Name: " studio ", Address: "10.0.0.2:7433", Fingerprint: strings.TrimPrefix(fp, "SHA256:")})
	must(t, err)
	// A known host is connected to from the start: it may be trying already.
	if st.Name != "studio" || st.Fingerprint != fp || (st.State != ipc.HostIdle && st.State != ipc.HostConnecting) || st.OurFingerprint != l.fingerprint() {
		t.Errorf("added = %+v", st)
	}
	st, err = l.c.AddHost(ipc.RemoteHostEdit{Name: "box", Address: "localhost:17433", Cert: certFile})
	must(t, err)
	if st.Fingerprint != fp || st.Cert != certFile {
		t.Errorf("added by cert = %+v", st)
	}
	if _, err := l.c.AddHost(ipc.RemoteHostEdit{Name: "box", Address: "a:1", Fingerprint: fp}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("a second box: %v", err)
	}
	saved, err := l.data.KnownHosts()
	must(t, err)
	if len(saved) != 2 || saved[0] != (config.KnownHost{Name: "studio", Address: "10.0.0.2:7433", Fingerprint: fp}) ||
		saved[1] != (config.KnownHost{Name: "box", Address: "localhost:17433", Fingerprint: fp, Cert: certFile}) {
		t.Errorf("data: %+v", saved)
	}
	if b, _ := os.ReadFile(l.cfg); string(b) != "# the laptop's own\n" {
		t.Errorf("config.toml was written:\n%s", b)
	}
	if hosts := l.c.Hosts(); len(hosts) != 2 || hosts[0].Name != "studio" || hosts[1].Name != "box" {
		t.Errorf("hosts = %+v", hosts)
	}

	// An edit: a rename moves the host's sessions here with it.
	l.c.mu.Lock()
	l.c.added["L09"] = &added{Added: Added{ID: "L09", Host: "box", RemoteID: "r1", Archived: true}}
	l.c.mu.Unlock()
	if _, err := l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "nope", Name: "x"}); !wire.IsCode(err, wire.CodeNotFound) {
		t.Errorf("edited a host that isn't there: %v", err)
	}
	// An edit from the host as it was before: refused, and nothing changes.
	before, err := l.c.Host("box")
	must(t, err)
	if _, err := l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "box", Address: "a:1", Version: "stale"}); !wire.IsCode(err, api.CodeConflict) {
		t.Errorf("a stale edit: %v", err)
	}
	if now, _ := l.c.Host("box"); now.Address != before.Address || now.Version != before.Version || now.Version == "" {
		t.Errorf("after a refused edit: %+v", now)
	}
	if _, err := l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "box", Name: "studio"}); err == nil {
		t.Error("renamed onto another host")
	}
	st, err = l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "box", Name: "tunnel", Address: "localhost:17434", Version: before.Version})
	must(t, err)
	if st.Name != "tunnel" || st.Address != "localhost:17434" || st.Cert != certFile || st.Version == before.Version {
		t.Errorf("edited = %+v", st)
	}
	l.c.mu.Lock()
	if l.c.added["L09"].Host != "tunnel" {
		t.Errorf("the session's host: %q", l.c.added["L09"].Host)
	}
	l.c.mu.Unlock()
	// A new fingerprint replaces the cert.
	other := "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	st, err = l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "tunnel", Fingerprint: other})
	must(t, err)
	if st.Fingerprint != other || st.Cert != "" {
		t.Errorf("new pin = %+v", st)
	}

	// Removing it takes its sessions from here; the host keeps them.
	must(t, l.c.RemoveHost(ctx5(), "tunnel"))
	if l.c.Is("L09") {
		t.Error("its session is still here")
	}
	if err := l.c.RemoveHost(ctx5(), "tunnel"); err == nil {
		t.Error("removed it twice")
	}
	if saved, _ := l.data.KnownHosts(); len(saved) != 1 || saved[0].Name != "studio" {
		t.Errorf("data: %+v", saved)
	}
	if _, err := l.c.Host("tunnel"); err == nil {
		t.Error("still known")
	}
	if _, err := l.c.Retry("tunnel"); err == nil {
		t.Error("retried a host that isn't known")
	}
}

func TestKnownHostsFollowConfig(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	// Added by hand, pinned by its cert alone.
	certFile, _ := identity.Files(filepath.Join(r.dir, "id"))
	text := fmt.Sprintf("# the laptop's own\n[[remote.hosts]]\nname = \"studio\"\naddress = %q\ncert = %q\n", r.addr(), certFile)
	must(t, os.WriteFile(l.cfg, []byte(text), 0o600))
	future := time.Now().Add(time.Second)
	must(t, os.Chtimes(l.cfg, future, future))
	must(t, l.c.Reload())
	list, err := l.c.Browse(ctx5(), "studio")
	must(t, err)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != "r1" || list.Sessions[0].Host != "studio" {
		t.Errorf("browse = %+v", list)
	}
	// It's read-only here: the file changes it.
	if h := l.c.Hosts(); len(h) != 1 || !h[0].InConfig {
		t.Errorf("hosts = %+v", h)
	}
	if _, err := l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "studio", Address: "a:1"}); !errors.Is(err, ErrInConfig) {
		t.Errorf("edit: %v", err)
	}
	if err := l.c.RemoveHost(ctx5(), "studio"); !errors.Is(err, ErrInConfig) {
		t.Errorf("remove: %v", err)
	}
	if _, err := l.c.AddHost(ipc.RemoteHostEdit{Name: "studio", Address: "a:1", Fingerprint: r.h.Status().Fingerprint}); err == nil {
		t.Error("added a second studio")
	}
	// A file that doesn't parse leaves the hosts as they are.
	must(t, os.WriteFile(l.cfg, []byte("x = "), 0o600))
	must(t, os.Chtimes(l.cfg, future.Add(time.Second), future.Add(time.Second)))
	must(t, l.c.Reload())
	if len(l.c.Hosts()) != 1 {
		t.Errorf("hosts = %+v", l.c.Hosts())
	}
	// Taken out by hand: disconnected and forgotten.
	must(t, os.WriteFile(l.cfg, []byte("# none\n"), 0o600))
	must(t, os.Chtimes(l.cfg, future.Add(2*time.Second), future.Add(2*time.Second)))
	must(t, l.c.Reload())
	if len(l.c.Hosts()) != 0 {
		t.Errorf("hosts = %+v", l.c.Hosts())
	}
	if _, err := l.c.Browse(ctx5(), "studio"); err == nil {
		t.Error("browsed a host that isn't known")
	}
}

func TestTwoHostsAtOnce(t *testing.T) {
	l := newLaptop(t)
	hosts := []*remoteHost{newRemoteHost(t, "studio"), newRemoteHost(t, "box")}
	var panes []*pane
	for i, r := range hosts {
		api1, web, private := r.name+"-1", r.name+"-2", r.name+"-3"
		r.authorize(l.fingerprint())
		r.b.add(remoteSession(api1, "api"))
		r.b.add(remoteSession(web, "web"))
		r.b.add(core.Session{ID: private, Name: "private", Status: core.StatusBusy, CreatedAt: t0})
		r.terminal(api1)
		l.know(r)

		// The whole host, Remote or not, as its tree says: each of its
		// sessions has a socket here, under the host's own id.
		list, err := l.c.Browse(ctx5(), r.name)
		must(t, err)
		if len(list.Sessions) != 3 || list.Sessions[0].Host != r.name {
			t.Fatalf("%s: browse = %+v", r.name, list)
		}
		eventually(t, r.name+"'s tree", func() bool { return l.c.Is(api1) && l.c.Is(private) })
		if h, ok := l.c.HostOf(api1); !ok || h != r.name {
			t.Errorf("HostOf %s = %q %v", api1, h, ok)
		}
		if st := l.state(r.name); st.State != ipc.HostConnected || st.Sessions != 3 || st.HostVersion != "1.0.0" {
			t.Errorf("%s: %+v", r.name, st)
		}
		// Typed in through the socket that's where a local host's would be.
		p := attachPane(t, l.o.Socket(api1))
		want := fmt.Sprintf("on-%s-%d", r.name, i+1)
		typed := fmt.Sprintf("echo on-%s-$((%d+1))\n", r.name, i)
		if runtime.GOOS == "windows" {
			typed = want + "\n" // the stand-in terminal shows what's typed
		}
		p.in <- []byte(typed)
		eventually(t, want, func() bool { return strings.Contains(p.out.String(), want) })
		panes = append(panes, p)
	}
	if !strings.Contains(panes[0].out.String(), "on-studio-1") || strings.Contains(panes[0].out.String(), "on-box") {
		t.Errorf("the panes mixed:\n%s", panes[0].out)
	}
	tree := l.c.Tree()
	if len(tree.Sessions) != 6 || tree.Sessions[0].Host != "studio" || tree.Sessions[5].Host != "box" {
		t.Errorf("tree = %+v", tree.Sessions)
	}
}

func TestPendingThenApproved(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.b.add(remoteSession("r1", "api"))
	l.know(r)
	_, err := l.c.Browse(ctx5(), "studio")
	if err == nil || !strings.Contains(err.Error(), "waiting for studio to approve") || !strings.Contains(err.Error(), l.fingerprint()) {
		t.Fatalf("browse before approval: %v", err)
	}
	st := l.waitState("studio", ipc.HostPending)
	if st.State != ipc.HostPending || st.OurFingerprint != l.fingerprint() || st.RetryAt.IsZero() {
		t.Errorf("pending = %+v", st)
	}
	reqs := r.h.Requests()
	if len(reqs) != 1 || reqs[0].Fingerprint != l.fingerprint() {
		t.Fatalf("requests = %+v", reqs)
	}
	_, err = r.h.Approve(reqs[0].ID, "laptop")
	must(t, err)
	// The next try gets in: still wanted, as it was browsed.
	l.waitState("studio", ipc.HostConnected)
	_, err = l.c.Browse(ctx5(), "studio")
	must(t, err)
}

func TestFingerprintMismatchIsAHardStop(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	wrong := "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err := l.c.AddHost(ipc.RemoteHostEdit{Name: "studio", Address: r.addr(), Fingerprint: wrong})
	must(t, err)
	_, err = l.c.Browse(ctx5(), "studio")
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("browse: %v", err)
	}
	st := l.waitState("studio", ipc.HostFingerprint)
	if st.State != ipc.HostFingerprint || st.Seen != r.fingerprint() || !st.RetryAt.IsZero() {
		t.Errorf("state = %+v", st)
	}
	// Not tried again by itself, nor by another use.
	n := l.dials.count()
	time.Sleep(300 * time.Millisecond)
	if _, err := l.c.Browse(ctx5(), "studio"); err == nil {
		t.Error("browsed")
	}
	if got := l.dials.count(); got != n {
		t.Errorf("dialed %d more times", got-n)
	}
	// Retry tries once more, and stops again.
	_, err = l.c.Retry("studio")
	must(t, err)
	eventually(t, "the retry", func() bool { return l.dials.count() == n+1 && l.state("studio").State == ipc.HostFingerprint })
	// The right fingerprint gets in.
	_, err = l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "studio", Fingerprint: r.fingerprint()})
	must(t, err)
	_, err = l.c.Browse(ctx5(), "studio")
	must(t, err)
}

func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		err   error
		state string
	}{
		{&wire.VersionError{Ours: wire.Hello{Proto: 1}, Theirs: wire.Hello{Proto: 2, Name: "studio"}}, ipc.HostVersion},
		{&apiError{theirs: 2, version: "9.0.0"}, ipc.HostVersion},
		{fmt.Errorf("%w: too many requests", wire.ErrUnauthorized), ipc.HostUnauthorized},
		{errors.New("connection refused"), ipc.HostUnreachable},
	} {
		l := newLaptop(t, func(o *Options) {
			o.Dial = func(context.Context, string, *identity.Identity, string, wire.Hello) (*wire.Conn, error) {
				return nil, tc.err
			}
		})
		_, err := l.c.AddHost(ipc.RemoteHostEdit{Name: "studio", Address: "127.0.0.1:1", Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
		must(t, err)
		_, err = l.c.Browse(ctx5(), "studio")
		if err == nil {
			t.Fatalf("%v: browsed", tc.err)
		}
		st := l.waitState("studio", tc.state)
		if st.State != tc.state || !strings.Contains(st.Error, tc.err.Error()) {
			t.Errorf("%v: state = %+v", tc.err, st)
		}
	}
	if !strings.Contains((&apiError{theirs: 2, version: "9.0.0"}).Error(), "version 2 (acmd 9.0.0)") {
		t.Error("the API error doesn't say the versions")
	}
}

func TestAnUnreachableHost(t *testing.T) {
	l := newLaptop(t, func(o *Options) { o.DialTimeout = 500 * time.Millisecond })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	addr := ln.Addr().String()
	_ = ln.Close()
	_, err = l.c.AddHost(ipc.RemoteHostEdit{Name: "studio", Address: addr, Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	must(t, err)
	if _, err := l.c.Harnesses(ctx5(), "studio"); err == nil {
		t.Fatal("reached nothing")
	}
	st := l.waitState("studio", ipc.HostUnreachable)
	if st.State != ipc.HostUnreachable || st.Error == "" {
		t.Errorf("state = %+v", st)
	}
	// It keeps trying while it's wanted, with a backoff.
	n := l.dials.count()
	eventually(t, "retries", func() bool { return l.dials.count() >= n+2 })
	// A call that can't wait gives up.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	l.c.mu.Lock()
	l.c.hosts["studio"].tries = 1 << 30 // no try ends while it waits
	l.c.mu.Unlock()
	if _, err := l.c.connect(ctx, "studio"); err == nil {
		t.Error("connected")
	}
}

func TestKnownHostsStayConnected(t *testing.T) {
	l := newLaptop(t, func(o *Options) { o.IdleAfter = 200 * time.Millisecond })
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	h, err := l.c.Harnesses(ctx5(), "studio")
	must(t, err)
	if h.Default != "hc" || len(h.Harnesses) != 1 || !h.Terminal {
		t.Errorf("harnesses = %+v", h)
	}
	// The host daemon's own ops, as acmd --host runs them.
	raw, err := l.c.Call(ctx5(), "studio", ipc.OpSnapshot, nil)
	must(t, err)
	if !strings.Contains(string(raw), `"sessions"`) {
		t.Errorf("call = %s", raw)
	}
	if _, err := l.c.Call(ctx5(), "studio", ipc.OpSpeechSay, nil); err == nil {
		t.Error("the host's own speech ran from here")
	}
	// A host that doesn't list call (an older acmd) says so, rather than failing oddly.
	l.c.mu.Lock()
	hc, _ := l.c.hostLocked("studio")
	hc.ops = []string{wire.OpPing}
	l.c.mu.Unlock()
	var ce *wire.CallError
	if _, err := l.c.Call(ctx5(), "studio", ipc.OpSnapshot, nil); !errors.As(err, &ce) || ce.Code != ipc.CodeNotSupported ||
		!strings.Contains(ce.Message, "update it") {
		t.Errorf("an older host: %v", err)
	}
	// A known host stays connected, to show its tree: idle or not.
	l.waitState("studio", ipc.HostConnected)
	time.Sleep(400 * time.Millisecond)
	if st := l.state("studio"); st.State != ipc.HostConnected {
		t.Errorf("went idle: %+v", st)
	}
}

func TestFolders(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	f, err := l.c.MakeFolder(ctx5(), ipc.RemoteFolderRequest{Host: "studio", Path: "~/src"})
	must(t, err)
	if f.Path != filepath.Join(r.home, "src") {
		t.Errorf("mkdir = %+v", f)
	}
	list, err := l.c.ListFolders(ctx5(), ipc.RemoteFolderRequest{Host: "studio", Path: "~"})
	must(t, err)
	if len(list.Folders) != 1 || list.Folders[0].Name != "src" {
		t.Errorf("list = %+v", list)
	}
	_, err = l.c.MakeFolder(ctx5(), ipc.RemoteFolderRequest{Host: "studio", Path: "~/src"})
	if !wire.IsCode(err, api.CodeExists) || !strings.HasPrefix(err.Error(), "studio: ") {
		t.Errorf("mkdir twice: %v", err)
	}
	r.b.add(core.Session{ID: "r1", Cwd: f.Path, Remote: true, Status: core.StatusDone, CreatedAt: t0})
	recent, err := l.c.RecentFolders(ctx5(), "studio")
	must(t, err)
	if len(recent.Folders) != 1 || recent.Folders[0].Path != f.Path {
		t.Errorf("recent = %+v", recent)
	}
}

func TestNewSessionOnAHost(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	s, err := l.c.NewSession(ctx5(), ipc.RemoteNewRequest{Host: "studio", Dir: "~", HarnessKind: core.HarnessTerminal, Name: "fresh"})
	must(t, err)
	made := r.b.session(s.ID)
	if made.Name != "fresh" || made.Cwd != r.home || s.Host != "studio" || s.RemoteID != s.ID {
		t.Errorf("on the host: %+v, here %+v", made, s)
	}
	eventually(t, "it in the host's tree", func() bool { return l.c.Is(s.ID) })
	if _, err := l.c.NewSession(ctx5(), ipc.RemoteNewRequest{Host: "studio", Dir: "~", HarnessKind: "emacs"}); !wire.IsCode(err, wire.CodeNotFound) &&
		!wire.IsCode(err, wire.CodeBadRequest) {
		t.Errorf("an unknown harness: %v", err)
	}
	if _, err := l.c.NewSession(ctx5(), ipc.RemoteNewRequest{Host: "nowhere", Dir: "~"}); err == nil {
		t.Error("a host that isn't known")
	}
}

// The host's tree: its sessions, groups and sets as it has them, each named by
// its host, with a socket for each of its own live sessions, its inbox under
// ids of their own here, and its notices.
func TestTheHostsTree(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	r.b.add(remoteSession("r2", "web"))
	r.b.change(func() {
		r.b.groups = []*core.Group{{ID: "g1", Name: "Backend", SortKey: 1}, {ID: "default", Name: "Default", IsDefault: true}}
		r.b.sets = []ipc.Set{{Type: ipc.EntitySet, ID: "set1", Parent: "g1"}, {Type: ipc.EntitySet, ID: "theirs", Parent: "g1", Host: "far"}}
		r.b.inbox = []core.InboxItem{{ID: 7, SessionID: "r1", Kind: core.InboxQuestion, Text: "Which?", Options: []string{"a", "b"}, State: core.InboxOpen, At: t0}}
		// One the host uses from yet another host: that host's to offer.
		r.b.sessions = append(r.b.sessions, &core.Session{ID: "x1", Host: "far", RemoteID: "f1", Status: core.StatusBusy, CreatedAt: t0})
	})
	l.know(r)
	eventually(t, "the tree", func() bool { return l.c.Is("r1") && l.c.Is("r2") })
	tree := l.c.Tree()
	if len(tree.Sessions) != 2 || tree.Sessions[0].Host != "studio" || tree.Sessions[0].RemoteID != "r1" {
		t.Errorf("sessions = %+v", tree.Sessions)
	}
	if len(tree.Groups) != 2 || tree.Groups[0].Host != "studio" || tree.Groups[0].Name != "Backend" || len(tree.Sets) != 1 || tree.Sets[0].Host != "studio" {
		t.Errorf("groups %+v, sets %+v", tree.Groups, tree.Sets)
	}
	for id, want := range map[string]bool{"r1": true, "g1": true, "set1": true, "x1": false, "theirs": false, "nope": false, "": false} {
		if _, ok := l.c.HostOf(id); ok != want {
			t.Errorf("HostOf(%q) = %v", id, ok)
		}
	}
	if l.c.Is("x1") {
		t.Error("a socket for another host's session")
	}

	// The inbox, named with the host, with the host's own id: nothing here
	// renumbers it (design 7.4).
	eventually(t, "the inbox", func() bool { return len(l.c.Inbox()) == 1 })
	it := l.c.Inbox()[0]
	if it.ID != 7 || it.SessionID != "r1" || it.Host != "studio" || it.Text != "Which?" || it.State != core.InboxOpen {
		t.Errorf("inbox = %+v", it)
	}
	if got, ok := l.c.InboxItem("studio", 7); !ok || got.Text != "Which?" {
		t.Errorf("item = %+v %v", got, ok)
	}
	if _, ok := l.c.InboxItem("other", 7); ok {
		t.Error("an item found on a host it isn't on")
	}
	if items := l.c.SessionInbox("studio", "r1"); len(items) != 1 || len(l.c.SessionInbox("studio", "r2")) != 0 {
		t.Errorf("session inbox = %+v", items)
	}
	// Read and handled on the host: here, it shows as the host has it.
	r.b.change(func() {
		r.b.inbox[0].Read = true
		r.b.inbox = append(r.b.inbox, core.InboxItem{ID: 8, SessionID: "r1", Kind: core.InboxDone, Text: "Done", State: core.InboxOpen, At: t0.Add(time.Minute)})
	})
	eventually(t, "two items", func() bool { return len(l.c.Inbox()) == 2 && l.c.Inbox()[0].Read })
	if items := l.c.Inbox(); items[0].ID != 7 || items[1].ID != 8 || items[1].Read || items[1].Text != "Done" {
		t.Errorf("inbox = %+v", items)
	}
	r.b.change(func() { r.b.inbox[0].State = core.InboxDismissed })
	eventually(t, "the dismiss", func() bool { items := l.c.Inbox(); return len(items) == 1 && items[0].Text == "Done" })

	// Its state follows.
	r.b.edit("r1", func(s *core.Session) { s.Status, s.StatusDetail, s.Unseen = core.StatusWaiting, "question", true })
	eventually(t, "waiting", func() bool {
		for _, s := range l.c.Tree().Sessions {
			if s.ID == "r1" {
				return s.Status == core.StatusWaiting && s.StatusDetail == "question" && s.Unseen
			}
		}
		return false
	})

	// Its notices.
	r.b.change(func() {
		r.b.notices = append(r.b.notices, ipc.Notice{Seq: 2, SessionID: "r1", Kind: "waiting", Title: "⚠ api needs you"})
	})
	eventually(t, "the notice", func() bool {
		n := l.b.noticesSoFar()
		return len(n) == 1 && n[0] == "r1 studio waiting ⚠ api needs you"
	})

	// Archived there: no socket here, and its items go.
	r.b.edit("r1", func(s *core.Session) { s.Status = core.StatusArchived })
	eventually(t, "no socket", func() bool { return !l.c.Is("r1") })
	if _, err := os.Stat(l.o.Socket("r1")); !os.IsNotExist(err) {
		t.Errorf("its socket: %v", err)
	}
	if len(l.c.Inbox()) != 0 {
		t.Errorf("inbox = %+v", l.c.Inbox())
	}
	// Gone there: gone here.
	r.b.change(func() { r.b.sessions = r.b.sessions[1:2] }) // only r2
	eventually(t, "gone", func() bool { return len(l.c.Tree().Sessions) == 1 })
	// After a reconnect, the snapshot.
	r.b.add(remoteSession("r3", "new"))
	r.restart(0)
	eventually(t, "the snapshot after a reconnect", func() bool { return l.c.Is("r3") })
}

func TestReconnectAndReattach(t *testing.T) {
	realShell(t)
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	sock := r.terminal("r1")
	l.know(r)
	eventually(t, "its socket", func() bool { return l.c.Is("r1") })
	id := "r1"
	p := attachPane(t, l.o.Socket(id))
	p.in <- []byte("echo first-$((1+1))\n")
	eventually(t, "first", func() bool { return strings.Contains(p.out.String(), "first-2") })

	// The host's daemon goes, and comes back: the pane reattaches on its
	// own, and the replay redraws it.
	r.h.Close()
	l.waitState("studio", ipc.HostUnreachable)
	// While it's away, a new pane waits.
	waiting := attachPane(t, l.o.Socket(id))
	must(t, term.Send(sock, []byte("echo meanwhile-$((2+2))\n")))
	time.Sleep(100 * time.Millisecond)
	r.start()
	l.waitState("studio", ipc.HostConnected)
	eventually(t, "the replay", func() bool { return strings.Count(p.out.String(), "meanwhile-4") >= 1 })
	eventually(t, "the waiting pane", func() bool { return strings.Contains(waiting.out.String(), "meanwhile-4") })
	p.in <- []byte("echo again-$((3+3))\n")
	eventually(t, "typed after", func() bool { return strings.Contains(p.out.String(), "again-6") })
	select {
	case err := <-p.done:
		t.Fatalf("the pane ended: %v", err)
	default:
	}

	// The terminal ending on the host ends the pane.
	must(t, term.Kill(sock))
	select {
	case err := <-p.done:
		if !errors.Is(err, term.ErrEnded) {
			t.Errorf("ended with %v", err)
		}
		p.done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("the pane didn't end with its terminal")
	}
}

func TestProxySocket(t *testing.T) {
	realShell(t)
	l := newLaptop(t, func(o *Options) { o.DialTimeout = time.Second })
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	sock := r.terminal("r1")
	l.know(r)
	eventually(t, "its socket", func() bool { return l.c.Is("r1") })
	id := "r1"
	proxy := l.o.Socket(id)
	info, err := os.Stat(proxy)
	must(t, err)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}

	// What the daemon asks a host's terminal works through it: a liveness
	// check, and typing without attaching.
	if !term.Running(proxy) {
		t.Error("not running")
	}
	must(t, term.Send(proxy, []byte("echo sent-$((4+4))\n")))
	eventually(t, "sent", func() bool {
		text, _ := term.ScreenText(sock)
		return strings.Contains(text, "sent-8")
	})
	text, err := term.ScreenText(proxy)
	must(t, err)
	if !strings.Contains(text, "sent-8") {
		t.Errorf("screen through the proxy:\n%s", text)
	}

	// Archived on the host: its socket goes; brought back: it's there again.
	r.b.edit("r1", func(s *core.Session) { s.Status = core.StatusArchived })
	eventually(t, "the socket gone", func() bool { _, err := os.Stat(proxy); return os.IsNotExist(err) })
	r.b.edit("r1", func(s *core.Session) { s.Status = core.StatusRunning })
	eventually(t, "back", func() bool { return term.Running(proxy) })

	// A restart of the client opens it again, as the tree comes.
	l.c.Close()
	l.start()
	eventually(t, "reopened", func() bool { return term.Running(proxy) })
	if !l.c.Is(id) || l.c.Is("L99") {
		t.Error("Is")
	}

	// A one-off to a host that's gone gives up.
	r.h.Close()
	l.waitState("studio", ipc.HostUnreachable)
	start := time.Now()
	if err := term.Send(proxy, []byte("x")); err != nil {
		t.Logf("send: %v", err)
	}
	if text, _ := term.ScreenText(proxy); text != "" {
		t.Errorf("read a screen from nowhere: %q", text)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestProxyChecksThePeer(t *testing.T) {
	l := newLaptop(t, func(o *Options) { o.CheckPeer = func(net.Conn) error { return errors.New("another user") } })
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("L01", "api"))
	l.know(r)
	eventually(t, "its socket", func() bool { return l.c.Is("L01") })
	c, err := net.Dial("unix", l.o.Socket("L01"))
	must(t, err)
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("read from a socket that refused us")
	}
}

func TestBackoff(t *testing.T) {
	lo, hi := 500*time.Millisecond, 30*time.Second
	for n, want := range map[int]time.Duration{1: lo, 2: time.Second, 3: 2 * time.Second, 7: hi, 100: hi} {
		if got := backoff(n, lo, hi); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
	for range 100 {
		if j := jitter(time.Second); j < 500*time.Millisecond || j > time.Second {
			t.Fatalf("jitter = %v", j)
		}
	}
	if jitter(1) != 1 {
		t.Error("jitter(1)")
	}
}

func TestClientIdentityError(t *testing.T) {
	l := newLaptop(t)
	text := "[remote.client]\ncert = \"/nonexistent/cert.pem\"\nkey = \"/nonexistent/key.pem\"\n[[remote.hosts]]\nname = \"studio\"\naddress = \"127.0.0.1:1\"\nfingerprint = \"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"\n"
	must(t, os.WriteFile(l.cfg, []byte(text), 0o600))
	must(t, l.c.Reload())
	if _, err := l.c.Browse(ctx5(), "studio"); err == nil || !strings.Contains(err.Error(), "client identity") {
		t.Errorf("browse: %v", err)
	}
	l.waitState("studio", ipc.HostError) // between its tries
}

// A host's settings, through it: the host ones, and changes to them.
func TestHostSettings(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	_, err := l.c.AddHost(ipc.RemoteHostEdit{Name: "studio", Address: r.addr(), Fingerprint: r.h.Status().Fingerprint})
	must(t, err)
	st, err := l.c.Config(ctx5(), "studio")
	must(t, err)
	if st.Values["remote.host.enabled"] != true {
		t.Fatalf("config = %+v", st.Values)
	}
	if _, ok := st.Values["editor.preset"]; ok {
		t.Error("saw a device setting")
	}
	loaded := st.Versions["default_harness"]
	st, err = l.c.SetConfig(ctx5(), "studio", ipc.ConfigSet{Values: map[string]any{"default_harness": "antigravity"},
		Versions: map[string]string{"default_harness": loaded}})
	must(t, err)
	if st.Values["default_harness"] != "antigravity" || r.file.Config().DefaultHarness != "antigravity" {
		t.Errorf("after set: %v", st.Values["default_harness"])
	}
	if st.Versions["default_harness"] == loaded || len(st.Versions) != len(st.Values) {
		t.Errorf("versions after set: %v", st.Versions)
	}
	// From what it was before that change: refused, and the host's stays.
	_, err = l.c.SetConfig(ctx5(), "studio", ipc.ConfigSet{Values: map[string]any{"default_harness": "claude"},
		Versions: map[string]string{"default_harness": loaded}})
	if !wire.IsCode(err, api.CodeConflict) || r.file.Config().DefaultHarness != "antigravity" {
		t.Errorf("a stale change: %v", err)
	}
	if _, err := l.c.SetConfig(ctx5(), "studio", ipc.ConfigSet{Values: map[string]any{"speech.voice": "af_bella"}}); err == nil {
		t.Error("changed a device setting")
	}
	if _, err := l.c.Config(ctx5(), "nowhere"); err == nil {
		t.Error("a host that isn't known")
	}
}

// This machine's presence goes to each host: now, when it changes, and to a
// host when it connects. An op carries the UI that asked.
func TestPresenceAndTheUIGoToTheHost(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	calls := func() string {
		r.b.mu.Lock()
		defer r.b.mu.Unlock()
		return strings.Join(r.b.called, "\n")
	}
	l.c.SetPresence(ipc.PresenceActive) // before any host: kept for when one connects
	_, err := l.c.AddHost(ipc.RemoteHostEdit{Name: "studio", Address: r.addr(), Fingerprint: r.h.Status().Fingerprint})
	must(t, err)
	eventually(t, "told on connecting", func() bool { return strings.Contains(calls(), `client.presence {"presence":"active"}`) })
	l.c.SetPresence(ipc.PresenceActive) // the same: nothing new to tell
	l.c.SetPresence(ipc.PresenceIdle)
	eventually(t, "told of the change", func() bool { return strings.Contains(calls(), `client.presence {"presence":"idle"}`) })
	if n := strings.Count(calls(), `"presence":"active"`); n != 1 {
		t.Errorf("told active %d times", n)
	}
	ctx := ipc.WithClient(ctx5(), &ipc.ClientHello{ID: "ui-1", Kind: ipc.KindMac})
	_, err = l.c.Call(ctx, "studio", ipc.OpStatus, nil)
	must(t, err)
	if !strings.Contains(calls(), "status null from ui-1") && !strings.Contains(calls(), "status  from ui-1") {
		t.Errorf("the host got %s", calls())
	}
	if l.c.Fingerprint() != l.fingerprint() || (*Client)(nil).Fingerprint() != "" {
		t.Error("fingerprint")
	}
	(*Client)(nil).SetPresence(ipc.PresenceActive)
}
