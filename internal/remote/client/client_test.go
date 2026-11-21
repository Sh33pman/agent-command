package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	return core.Session{ID: id, Name: name, Cwd: "/w/" + name, Harness: core.HarnessTerminal, Status: core.StatusRunning,
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
	if st.Name != "studio" || st.Fingerprint != fp || st.State != ipc.HostIdle || st.OurFingerprint != l.fingerprint() {
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

	// An edit: a rename moves the sessions added from it.
	// Under the locks: the hosts' goroutines read both.
	l.b.mu.Lock()
	l.b.sessions["L09"] = &core.Session{ID: "L09", Host: "box", RemoteID: "r1", Status: core.StatusArchived}
	l.b.mu.Unlock()
	l.c.mu.Lock()
	l.c.added["L09"] = &added{Added: Added{ID: "L09", Host: "box", RemoteID: "r1", Archived: true}}
	l.c.mu.Unlock()
	if _, err := l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "nope", Name: "x"}); err == nil {
		t.Error("edited a host that isn't there")
	}
	if _, err := l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "box", Name: "studio"}); err == nil {
		t.Error("renamed onto another host")
	}
	st, err = l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "box", Name: "tunnel", Address: "localhost:17434"})
	must(t, err)
	if st.Name != "tunnel" || st.Address != "localhost:17434" || st.Cert != certFile {
		t.Errorf("edited = %+v", st)
	}
	if l.b.get("L09").Host != "tunnel" {
		t.Errorf("the added session's host: %q", l.b.get("L09").Host)
	}
	// A new fingerprint replaces the cert.
	other := "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	st, err = l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "tunnel", Fingerprint: other})
	must(t, err)
	if st.Fingerprint != other || st.Cert != "" {
		t.Errorf("new pin = %+v", st)
	}

	// Removing it removes the sessions added from it, here only.
	must(t, l.c.RemoveHost(ctx5(), "tunnel"))
	if len(l.b.sessions) != 0 {
		t.Errorf("sessions left: %+v", l.b.sessions)
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
	if len(list.Sessions) != 1 || list.Sessions[0].ID != "r1" || list.Sessions[0].Added {
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
		r.authorize(l.fingerprint())
		r.b.add(remoteSession("r1", "api"))
		r.b.add(remoteSession("r2", "web"))
		r.b.add(core.Session{ID: "l1", Name: "private", Status: core.StatusBusy, CreatedAt: t0})
		r.terminal("r1")
		l.know(r)

		list, err := l.c.Browse(ctx5(), r.name)
		must(t, err)
		if len(list.Sessions) != 2 || list.Sessions[0].Added {
			t.Fatalf("%s: browse = %+v", r.name, list)
		}
		id, err := l.c.AddSession(ctx5(), r.name, "r1", "work")
		must(t, err)
		again, err := l.c.AddSession(ctx5(), r.name, "r1", "")
		must(t, err)
		if again != id {
			t.Errorf("added twice: %s, %s", id, again)
		}
		s := l.b.get(id)
		if s.Host != r.name || s.RemoteID != "r1" || s.Name != "api" || s.Cwd != "/w/api" || s.GroupID != "work" ||
			s.Kind != core.KindHosted || s.Status != core.StatusRunning {
			t.Errorf("added = %+v", s)
		}
		if _, err := l.c.AddSession(ctx5(), r.name, "l1", ""); err == nil {
			t.Error("added a session that isn't Remote")
		}
		list, err = l.c.Browse(ctx5(), r.name)
		must(t, err)
		for _, s := range list.Sessions {
			if s.Added != (s.ID == "r1") || (s.Added && s.LocalID != id) {
				t.Errorf("%s: browse = %+v", r.name, s)
			}
		}
		if st := l.state(r.name); st.State != ipc.HostConnected || st.Sessions != 1 || st.HostVersion != "1.0.0" {
			t.Errorf("%s: %+v", r.name, st)
		}
		// Typed in through the socket that's where a local host's would be.
		p := attachPane(t, l.o.Socket(id))
		p.in <- []byte(fmt.Sprintf("echo on-%s-$((%d+1))\n", r.name, i))
		want := fmt.Sprintf("on-%s-%d", r.name, i+1)
		eventually(t, want, func() bool { return strings.Contains(p.out.String(), want) })
		panes = append(panes, p)
	}
	if !strings.Contains(panes[0].out.String(), "on-studio-1") || strings.Contains(panes[0].out.String(), "on-box") {
		t.Errorf("the panes mixed:\n%s", panes[0].out)
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

func TestIdleHostDisconnects(t *testing.T) {
	l := newLaptop(t, func(o *Options) { o.IdleAfter = 200 * time.Millisecond })
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	h, err := l.c.Harnesses(ctx5(), "studio")
	must(t, err)
	if h.Default != core.HarnessClaude || len(h.Harnesses) != 2 {
		t.Errorf("harnesses = %+v", h)
	}
	l.waitState("studio", ipc.HostConnected)
	l.waitState("studio", ipc.HostIdle)
	eventually(t, "the host sees it go", func() bool { return r.h.Status().Connected == 0 })
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
	id, err := l.c.NewSession(ctx5(), ipc.RemoteNewRequest{Host: "studio", Dir: "~", Harness: core.HarnessTerminal, Name: "fresh", GroupID: "g1"})
	must(t, err)
	made := r.b.session("new1")
	if !made.Remote || made.Name != "fresh" || made.Cwd != r.home {
		t.Errorf("on the host: %+v", made)
	}
	s := l.b.get(id)
	if s.Host != "studio" || s.RemoteID != "new1" || s.Name != "fresh" || s.GroupID != "g1" {
		t.Errorf("added: %+v", s)
	}
	if _, err := l.c.NewSession(ctx5(), ipc.RemoteNewRequest{Host: "studio", Dir: "~", Harness: "emacs"}); !wire.IsCode(err, wire.CodeBadRequest) {
		t.Errorf("an unknown harness: %v", err)
	}
	l.b.failAdd = errors.New("disk full")
	if _, err := l.c.NewSession(ctx5(), ipc.RemoteNewRequest{Host: "studio", Dir: "~"}); err == nil {
		t.Error("added without the store")
	}
}

func TestRenameAndEndGoToTheHost(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	r.b.add(remoteSession("r2", "web"))
	l.know(r)
	id, err := l.c.AddSession(ctx5(), "studio", "r1", "")
	must(t, err)
	must(t, l.c.Rename(ctx5(), id, "renamed"))
	if r.b.session("r1").Name != "renamed" || l.b.get(id).Name != "renamed" {
		t.Errorf("rename: host %q, here %q", r.b.session("r1").Name, l.b.get(id).Name)
	}
	if err := l.c.Rename(ctx5(), "L99", "x"); err == nil {
		t.Error("renamed a session that isn't added")
	}
	if err := l.c.End(ctx5(), "L99", false); err == nil {
		t.Error("ended a session that isn't added")
	}
	two, err := l.c.AddSession(ctx5(), "studio", "r2", "")
	must(t, err)
	must(t, l.c.End(ctx5(), two, true))
	eventually(t, "finished, here too", func() bool { return l.b.get(two).Status == core.StatusFinished })
	must(t, l.c.End(ctx5(), id, false))
	if r.b.session("r1").Status != core.StatusArchived {
		t.Errorf("end: %+v", r.b.session("r1"))
	}
	// Archived there: no longer offered.
	eventually(t, "gone", func() bool { return l.b.get(id).HostGone })
}

func TestEventsMerge(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	r.b.add(remoteSession("r2", "web"))
	r.b.change(func() {
		r.b.inbox = []core.InboxItem{{ID: 7, SessionID: "r1", Kind: core.InboxQuestion, Text: "Which?", Options: []string{"a", "b"}, State: core.InboxOpen, At: t0}}
	})
	l.know(r)
	id, err := l.c.AddSession(ctx5(), "studio", "r1", "")
	must(t, err)
	two, err := l.c.AddSession(ctx5(), "studio", "r2", "")
	must(t, err)

	// The snapshot's inbox, named with the host, with an id of its own.
	eventually(t, "the inbox", func() bool { return len(l.c.Inbox()) == 1 })
	it := l.c.Inbox()[0]
	if it.ID >= 0 || it.SessionID != id || it.Host != "studio" || it.Text != "Which?" || it.State != core.InboxOpen {
		t.Errorf("inbox = %+v", it)
	}
	if got, ok := l.c.InboxItem(it.ID); !ok || got.Text != "Which?" {
		t.Errorf("item = %+v %v", got, ok)
	}
	if items := l.c.SessionInbox(id); len(items) != 1 || len(l.c.SessionInbox(two)) != 0 {
		t.Errorf("session inbox = %+v", items)
	}
	l.c.SetInboxRead([]int64{it.ID}, true)
	if !l.c.Inbox()[0].Read {
		t.Error("not read")
	}

	// Its state, and its unseen flag when the host's changes.
	r.b.edit("r1", func(s *core.Session) { s.Status, s.StatusDetail, s.Unseen = core.StatusWaiting, "question", true })
	eventually(t, "waiting", func() bool {
		s := l.b.get(id)
		return s.Status == core.StatusWaiting && s.StatusDetail == "question" && s.Unseen
	})
	r.b.edit("r1", func(s *core.Session) { s.Unseen = false })
	eventually(t, "seen", func() bool { return !l.b.get(id).Unseen })

	// A new item keeps the old one's id; read here stays read.
	r.b.change(func() {
		r.b.inbox = append(r.b.inbox, core.InboxItem{ID: 8, SessionID: "r1", Kind: core.InboxDone, Text: "Done", State: core.InboxOpen, At: t0.Add(time.Minute)})
	})
	eventually(t, "two items", func() bool { return len(l.c.Inbox()) == 2 })
	items := l.c.Inbox()
	if items[0].ID != it.ID || !items[0].Read || items[1].Read || items[1].Text != "Done" {
		t.Errorf("inbox = %+v", items)
	}
	l.c.HandleInbox([]int64{it.ID})
	if items := l.c.Inbox(); len(items) != 1 || items[0].Text != "Done" {
		t.Errorf("after the dismiss: %+v", items)
	}

	// Notices about added sessions, not others.
	r.b.add(remoteSession("r3", "other"))
	r.b.change(func() {
		r.b.notices = append(r.b.notices,
			ipc.Notice{Seq: 1, SessionID: "r3", Kind: "done", Title: "other"},
			ipc.Notice{Seq: 2, SessionID: "r1", Kind: "waiting", Title: "⚠ api needs you"})
	})
	eventually(t, "the notice", func() bool {
		l.b.mu.Lock()
		defer l.b.mu.Unlock()
		return len(l.b.notices) == 1 && l.b.notices[0] == id+" studio waiting ⚠ api needs you"
	})

	// Turned local on the host: no longer available, and its items go.
	r.b.edit("r1", func(s *core.Session) { s.Remote = false })
	eventually(t, "gone", func() bool { return l.b.get(id).HostGone })
	if len(l.c.Inbox()) != 0 {
		t.Errorf("inbox = %+v", l.c.Inbox())
	}
	// And back.
	r.b.edit("r1", func(s *core.Session) { s.Remote = true })
	eventually(t, "back", func() bool { return !l.b.get(id).HostGone })

	// Removed here: the host isn't touched.
	must(t, l.c.RemoveSession(ctx5(), two))
	if l.b.get(two).ID != "" || !r.b.session("r2").Remote {
		t.Errorf("remove: here %+v, there %+v", l.b.get(two), r.b.session("r2"))
	}
	if err := l.c.RemoveSession(ctx5(), two); err == nil {
		t.Error("removed it twice")
	}
	if _, err := os.Stat(l.o.Socket(two)); !os.IsNotExist(err) {
		t.Errorf("its socket: %v", err)
	}

	// A snapshot after a reconnect: what went meanwhile is gone.
	r.b.edit("r1", func(s *core.Session) { s.Status = core.StatusArchived })
	r.restart(0)
	eventually(t, "gone after the reconnect", func() bool { return l.b.get(id).HostGone })
}

func TestReconnectAndReattach(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	sock := r.terminal("r1")
	l.know(r)
	id, err := l.c.AddSession(ctx5(), "studio", "r1", "")
	must(t, err)
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
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	sock := r.terminal("r1")
	l.know(r)
	id, err := l.c.AddSession(ctx5(), "studio", "r1", "")
	must(t, err)
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

	// Archived here: its socket goes; brought back: it's there again.
	l.c.SetArchived(id, true)
	eventually(t, "the socket gone", func() bool { _, err := os.Stat(proxy); return os.IsNotExist(err) })
	eventually(t, "idle", func() bool { return l.state("studio").Sessions == 0 })
	l.c.SetArchived(id, false)
	if !term.Running(proxy) {
		t.Error("not back")
	}

	// A restart of the client opens it again.
	l.c.Close()
	l.start()
	if !term.Running(proxy) {
		t.Error("not reopened")
	}
	if !l.c.Is(id) || l.c.Is("L99") {
		t.Error("Is")
	}

	// A one-off to a host that's offline gives up.
	l2 := newLaptop(t, func(o *Options) { o.DialTimeout = 300 * time.Millisecond })
	l2.b.sessions["L01"] = &core.Session{ID: "L01", Host: "nowhere", RemoteID: "r1", Status: core.StatusRunning}
	l2.c.Close()
	l2.start()
	start := time.Now()
	if err := term.Send(l2.o.Socket("L01"), []byte("x")); err != nil {
		t.Logf("send: %v", err)
	}
	if text, _ := term.ScreenText(l2.o.Socket("L01")); text != "" {
		t.Errorf("read a screen from nowhere: %q", text)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestProxyChecksThePeer(t *testing.T) {
	l := newLaptop(t, func(o *Options) { o.CheckPeer = func(net.Conn) error { return errors.New("another user") } })
	l.b.sessions["L01"] = &core.Session{ID: "L01", Host: "studio", RemoteID: "r1", Status: core.StatusRunning}
	l.c.Close()
	l.start()
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

func TestLocal(t *testing.T) {
	s := Local("studio", api.Session{ID: "r1", Name: "api", Harness: "claude", Dir: "/w", Status: "done", Unseen: true, Created: t0, LastActivity: t0.Add(time.Hour)})
	if s.Host != "studio" || s.RemoteID != "r1" || s.Kind != core.KindHosted || s.NameSource != core.NameUser || !s.Unseen ||
		!s.CreatedAt.Equal(t0) || !s.LastEventAt.Equal(t0.Add(time.Hour)) || s.Status != core.StatusDone {
		t.Errorf("local = %+v", s)
	}
	s.Status, s.HostGone = core.StatusArchived, true
	Apply(s, api.Session{ID: "r1", Status: "busy"})
	if s.Status != core.StatusArchived || s.HostGone || s.Harness != "claude" || s.NameSource != core.NameUser {
		t.Errorf("an archived one = %+v", s)
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
	st, err = l.c.SetConfig(ctx5(), "studio", map[string]any{"default_harness": "antigravity"})
	must(t, err)
	if st.Values["default_harness"] != "antigravity" || r.file.Config().DefaultHarness != "antigravity" {
		t.Errorf("after set: %v", st.Values["default_harness"])
	}
	if _, err := l.c.SetConfig(ctx5(), "studio", map[string]any{"speech.voice": "af_bella"}); err == nil {
		t.Error("changed a device setting")
	}
	if _, err := l.c.Config(ctx5(), "nowhere"); err == nil {
		t.Error("a host that isn't known")
	}
}
