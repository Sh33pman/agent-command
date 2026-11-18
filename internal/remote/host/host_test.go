package host

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/setup"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// logs is a log that tests can read while the host writes to it.
type logs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type fixture struct {
	t       *testing.T
	dir     string
	config  string
	state   string
	port    int
	clock   *clock
	logs    *logs
	h       *Host
	changes int
	mu      sync.Mutex
}

// freePort is a port nothing listens on just now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// occupy listens on port until the test ends, or until the returned func.
func occupy(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return func() { _ = ln.Close() }
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// newFixture is a host with config.toml from the template, Hosting on at a
// free port unless enabled is false.
func newFixture(t *testing.T, enabled bool) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, dir: dir, config: filepath.Join(dir, "config.toml"), state: filepath.Join(dir, "state", "remote-host.json"),
		port: freePort(t), clock: &clock{t: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}, logs: &logs{}}
	f.set("remote.host", "enabled", enabled)
	f.set("remote.host", "port", f.port)
	f.h = f.newHost()
	return f
}

func (f *fixture) newHost() *Host {
	h := New(Options{ConfigFile: f.config, StateFile: f.state, IdentityDir: filepath.Join(f.dir, "identity"), Version: "0.59.0",
		Log: log.New(f.logs, "", 0), Now: f.clock.Now, OnChange: func() { f.mu.Lock(); f.changes++; f.mu.Unlock() }})
	f.t.Cleanup(h.Close)
	must(f.t, h.Reload())
	return h
}

func (f *fixture) set(section, key string, v any) {
	f.t.Helper()
	must(f.t, setup.SaveSetting(f.config, section, key, v))
}

func (f *fixture) addr() string {
	f.t.Helper()
	st := f.h.Status()
	if !st.Listening {
		f.t.Fatalf("not listening: %+v", st)
	}
	return st.Address
}

// client is another machine's identity.
func (f *fixture) client(name string) *identity.Identity {
	f.t.Helper()
	id, err := identity.Ensure(filepath.Join(f.dir, "clients", name))
	must(f.t, err)
	return id
}

func (f *fixture) dial(id *identity.Identity) (*wire.Conn, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := wire.Dial(ctx, f.addr(), id, f.h.Status().Fingerprint, wire.Hello{Version: "0.59.1", Name: "laptop"})
	if c != nil {
		f.t.Cleanup(func() { _ = c.Close() })
	}
	return c, err
}

func (f *fixture) authorize(id *identity.Identity, name string) {
	f.t.Helper()
	if _, err := f.h.Authorize(id.Fingerprint(), name); err != nil {
		f.t.Fatal(err)
	}
}

func waitDone(t *testing.T, c *wire.Conn, what string) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatalf("%s: the connection is still open", what)
	}
}

// eventually waits for cond, which the host reaches in its own time.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHostingOffByDefault(t *testing.T) {
	f := newFixture(t, false)
	st := f.h.Status()
	if st.Enabled || st.Listening || st.Error != "" || st.Fingerprint != "" || st.Listen != "localhost" || st.ConfiguredPort != f.port || !st.PortFallback || st.Proto != wire.Proto {
		t.Errorf("status = %+v", st)
	}
	// It's off without a config too, and can't be changed.
	h := New(Options{})
	defer h.Close()
	must(t, h.Reload())
	if st := h.Status(); st.Enabled || st.Listening || st.ConfiguredPort != config.DefaultRemotePort {
		t.Errorf("no config: %+v", st)
	}
	if _, err := h.SetHosting(true); !errors.Is(err, ErrNoConfig) {
		t.Errorf("SetHosting: %v", err)
	}
	if _, err := h.Authorize(f.client("a").Fingerprint(), ""); !errors.Is(err, ErrNoConfig) {
		t.Errorf("Authorize: %v", err)
	}
	if _, err := h.Revoke("a"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("Revoke: %v", err)
	}
	if _, err := h.info(context.Background(), nil); err == nil {
		t.Error("info with Hosting off")
	}
}

func TestAuthorizedClient(t *testing.T) {
	f := newFixture(t, true)
	st := f.h.Status()
	if !st.Listening || st.Address != "127.0.0.1:"+strconv.Itoa(f.port) || st.Port != f.port || st.Name == "" ||
		!st.Since.Equal(f.clock.Now()) || !strings.HasSuffix(st.Cert, "cert.pem") {
		t.Fatalf("status = %+v", st)
	}
	laptop := f.client("laptop")
	f.authorize(laptop, "laptop")
	c, err := f.dial(laptop)
	must(t, err)
	var info wire.Info
	must(t, c.Call(context.Background(), wire.OpInfo, nil, &info))
	if info.Fingerprint != st.Fingerprint || info.Name != st.Name || info.Version != "0.59.0" || info.Proto != wire.Proto ||
		strings.Join(info.Ops, ",") != "info,ping" {
		t.Errorf("info = %+v", info)
	}
	must(t, c.Call(context.Background(), wire.OpPing, nil, nil))

	clients := f.h.Clients()
	if len(clients) != 1 || clients[0].Name != "laptop" || len(clients[0].Connections) != 1 {
		t.Fatalf("clients = %+v", clients)
	}
	rc := clients[0].Connections[0]
	if rc.ID != "c1" || rc.Name != "laptop" || rc.Fingerprint != laptop.Fingerprint() || rc.CommonName != laptop.CommonName ||
		rc.Version != "0.59.1" || !strings.HasPrefix(rc.Addr, "127.0.0.1:") {
		t.Errorf("connection = %+v", rc)
	}
	if st := f.h.Status(); st.Connected != 1 || st.Authorized != 1 {
		t.Errorf("status = %+v", st)
	}

	logs := f.logs.String()
	for _, want := range []string{"Hosting on 127.0.0.1:" + strconv.Itoa(f.port), `"laptop" (` + laptop.Fingerprint() + ") at 127.0.0.1:", "connected (acmd 0.59.1)", "info from"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "ping from") || strings.Contains(logs, "PRIVATE KEY") {
		t.Errorf("logs:\n%s", logs)
	}
	_ = c.Close()
	eventually(t, "the disconnect", func() bool { return f.h.Status().Connected == 0 })
	if !strings.Contains(f.logs.String(), "disconnected") {
		t.Errorf("logs lack the disconnect:\n%s", f.logs)
	}
}

func TestUnknownClientApproved(t *testing.T) {
	f := newFixture(t, true)
	laptop := f.client("laptop")
	_, err := f.dial(laptop)
	if !errors.Is(err, wire.ErrPending) {
		t.Fatalf("unknown client: %v", err)
	}
	reqs := f.h.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v", reqs)
	}
	r := reqs[0]
	if r.Fingerprint != laptop.Fingerprint() || r.CommonName != laptop.CommonName || r.Count != 1 ||
		!r.FirstSeen.Equal(f.clock.Now()) || !strings.HasPrefix(r.Addr, "127.0.0.1:") || len(r.ID) != 6 {
		t.Errorf("request = %+v", r)
	}
	if !strings.Contains(err.Error(), "request "+r.ID+": approve it on the host, with acmd remote requests approve "+r.ID) {
		t.Errorf("message: %v", err)
	}

	// Trying again counts, in the same request.
	f.clock.Add(time.Minute)
	if _, err := f.dial(laptop); !errors.Is(err, wire.ErrPending) {
		t.Fatal(err)
	}
	reqs = f.h.Requests()
	if len(reqs) != 1 || reqs[0].ID != r.ID || reqs[0].Count != 2 || !reqs[0].LastSeen.Equal(f.clock.Now()) {
		t.Errorf("requests = %+v", reqs)
	}
	if st := f.h.Status(); st.Pending != 1 {
		t.Errorf("status = %+v", st)
	}
	// They survive a restart.
	f.h.Close()
	f.h = f.newHost()
	if reqs := f.h.Requests(); len(reqs) != 1 || reqs[0].ID != r.ID || reqs[0].Count != 2 {
		t.Errorf("after a restart: %+v", reqs)
	}

	if _, err := f.h.Approve("nope", ""); err == nil {
		t.Error("approved a request that doesn't exist")
	}
	c, err := f.h.Approve(r.ID, "")
	must(t, err)
	if c.Name != laptop.CommonName || c.Fingerprint != laptop.Fingerprint() {
		t.Errorf("approved = %+v", c)
	}
	if reqs := f.h.Requests(); len(reqs) != 0 {
		t.Errorf("the request is still there: %+v", reqs)
	}
	conn, err := f.dial(laptop)
	must(t, err)
	must(t, conn.Call(context.Background(), wire.OpPing, nil, nil))

	// It's in config.toml now, which kept its comments.
	b, err := os.ReadFile(f.config)
	must(t, err)
	if !strings.Contains(string(b), "# agent-command (acmd) configuration.") ||
		!strings.Contains(string(b), "[[remote.authorized_clients]]\nname = \""+laptop.CommonName+"\"\nfingerprint = \""+laptop.Fingerprint()+"\"\n") {
		t.Errorf("config.toml:\n%s", b)
	}
	// Reading config.toml again changes nothing, and keeps the connection.
	must(t, f.h.Reload())
	if st := f.h.Status(); st.Connected != 1 || st.Authorized != 1 {
		t.Errorf("after a reload: %+v", st)
	}
	logs := f.logs.String()
	for _, want := range []string{"unknown client " + laptop.Fingerprint(), "kept as request " + r.ID, "request " + r.ID + " again (2 tries)", "approved request " + r.ID} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
}

func TestDeny(t *testing.T) {
	f := newFixture(t, true)
	laptop := f.client("laptop")
	if _, err := f.dial(laptop); !errors.Is(err, wire.ErrPending) {
		t.Fatal(err)
	}
	id := f.h.Requests()[0].ID
	must(t, f.h.Deny(id))
	if reqs := f.h.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %+v", reqs)
	}
	if err := f.h.Deny(id); err == nil {
		t.Error("denied it twice")
	}
	// It's still refused, and asks again.
	if _, err := f.dial(laptop); !errors.Is(err, wire.ErrPending) {
		t.Fatal(err)
	}
	if reqs := f.h.Requests(); len(reqs) != 1 || reqs[0].ID == id {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestRevokeDropsClient(t *testing.T) {
	f := newFixture(t, true)
	ready := make(chan struct{}, 1)
	f.h.HandleStream("hold", func(ctx context.Context, _ *wire.Call, s *wire.Stream) error {
		_ = s.OK(nil)
		ready <- struct{}{}
		<-ctx.Done()
		return nil
	})
	laptop, desk := f.client("laptop"), f.client("desk")
	f.authorize(laptop, "laptop")
	f.authorize(desk, "desk")
	c, err := f.dial(laptop)
	must(t, err)
	other, err := f.dial(desk)
	must(t, err)
	st, err := c.Open(context.Background(), "hold", nil, nil)
	must(t, err)
	<-ready

	if _, err := f.h.Revoke("nobody"); err == nil {
		t.Error("revoked nobody")
	}
	got, err := f.h.Revoke("laptop")
	must(t, err)
	if got.Fingerprint != laptop.Fingerprint() {
		t.Errorf("revoked %+v", got)
	}
	waitDone(t, c, "revoked")
	if _, err := st.Read(make([]byte, 1)); err == nil {
		t.Error("the stream is still open")
	}
	// The other client stays.
	must(t, other.Call(context.Background(), wire.OpPing, nil, nil))
	// And the revoked one is unknown again.
	if _, err := f.dial(laptop); !errors.Is(err, wire.ErrPending) {
		t.Errorf("revoked client: %v", err)
	}
	b, _ := os.ReadFile(f.config)
	if strings.Contains(string(b), laptop.Fingerprint()) || !strings.Contains(string(b), desk.Fingerprint()) {
		t.Errorf("config.toml:\n%s", b)
	}
	if !strings.Contains(f.logs.String(), `revoked "laptop" (`+laptop.Fingerprint()+"): closed 1 connections") {
		t.Errorf("logs:\n%s", f.logs)
	}

	// Taken out of config.toml by hand: it's dropped on the next reload.
	f.clock.Add(time.Second)
	_, err = setup.RemoveAuthorizedClients(f.config, func(config.AuthorizedClient) bool { return true })
	must(t, err)
	must(t, f.h.Reload())
	waitDone(t, other, "removed from config.toml")

	// By fingerprint.
	f.authorize(desk, "desk")
	if _, err := f.h.Revoke(desk.Fingerprint()); err != nil {
		t.Error(err)
	}
}

func TestAuthorize(t *testing.T) {
	f := newFixture(t, true)
	laptop, desk := f.client("laptop"), f.client("desk")
	if _, err := f.dial(laptop); !errors.Is(err, wire.ErrPending) {
		t.Fatal(err)
	}
	// Authorizing by fingerprint drops its request.
	c, err := f.h.Authorize(strings.TrimPrefix(laptop.Fingerprint(), "SHA256:"), "")
	must(t, err)
	if c.Name != "client-"+strings.TrimPrefix(laptop.Fingerprint(), "SHA256:")[:8] || c.Fingerprint != laptop.Fingerprint() {
		t.Errorf("authorized %+v", c)
	}
	if reqs := f.h.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %+v", reqs)
	}
	if _, err := f.h.Authorize(laptop.Fingerprint(), "again"); !errors.Is(err, ErrAlreadyAuthorized) || !strings.Contains(err.Error(), c.Name) {
		t.Errorf("twice: %v", err)
	}
	if _, err := f.h.Authorize("nope", ""); err == nil {
		t.Error("a bad fingerprint")
	}
	// Names are unique.
	c2, err := f.h.Authorize(desk.Fingerprint(), c.Name)
	must(t, err)
	if c2.Name != c.Name+"-2" {
		t.Errorf("name = %q", c2.Name)
	}
	// Approving a client that's already on the list just drops its request.
	must(t, setup.SaveSetting(f.config, "x", "y", 1)) // config.toml changes on disk only
	f.h.mu.Lock()
	_, _, _ = f.h.recordLocked(wire.Peer{Fingerprint: desk.Fingerprint(), CommonName: "desk"})
	id := f.h.st.Requests[0].ID
	f.h.mu.Unlock()
	if got, err := f.h.Approve(id, ""); err != nil || got.Name != c2.Name {
		t.Errorf("approve = %+v, %v", got, err)
	}
	// A config.toml that doesn't parse.
	must(t, os.WriteFile(f.config, []byte("x = "), 0o600))
	if _, err := f.h.Authorize(f.client("third").Fingerprint(), ""); err == nil {
		t.Error("wrote to a bad config.toml")
	}
	if _, err := f.h.Revoke(c.Name); err == nil {
		t.Error("revoked in a bad config.toml")
	}
	if err := f.h.Reload(); err == nil || !f.h.Status().Listening {
		t.Errorf("a bad config.toml: %v, %+v", err, f.h.Status())
	}
	// Two with one name: revoking by name is ambiguous.
	f.h.mu.Lock()
	f.h.remote.AuthorizedClients = append(f.h.remote.AuthorizedClients, config.AuthorizedClient{Name: c.Name, Fingerprint: f.client("x").Fingerprint()})
	_, err = f.h.findClientLocked(c.Name)
	f.h.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "use the fingerprint") {
		t.Errorf("ambiguous: %v", err)
	}
}

func TestDisconnect(t *testing.T) {
	f := newFixture(t, true)
	laptop := f.client("laptop")
	f.authorize(laptop, "laptop")
	c, err := f.dial(laptop)
	must(t, err)
	if _, err := f.h.Disconnect("desk"); err == nil {
		t.Error("disconnected a client that isn't connected")
	}
	for _, sel := range []string{"laptop", "c2", laptop.Fingerprint()} {
		eventually(t, "the connection", func() bool { return f.h.Status().Connected == 1 })
		n, err := f.h.Disconnect(sel)
		if n != 1 || err != nil {
			t.Fatalf("%s: %d %v", sel, n, err)
		}
		// Gone at once, not once its goroutine notices: a second disconnect
		// finds nothing.
		if st := f.h.Status(); st.Connected != 0 || len(f.h.Clients()[0].Connections) != 0 {
			t.Errorf("%s: still connected: %+v", sel, st)
		}
		if _, err := f.h.Disconnect(sel); err == nil {
			t.Errorf("%s: disconnected twice", sel)
		}
		waitDone(t, c, sel)
		// It's still authorized, so it can come back.
		c, err = f.dial(laptop)
		must(t, err)
	}
}

func TestAttach(t *testing.T) {
	f := newFixture(t, true)
	released := make(chan struct{})
	f.h.HandleStream("attach", func(_ context.Context, c *wire.Call, s *wire.Stream) error {
		var args struct{ Session string }
		if err := c.Decode(&args); err != nil {
			return err
		}
		release := f.h.Attach(c.Conn, args.Session, s)
		defer release()
		defer close(released)
		_ = s.OK(nil)
		_, err := io.Copy(io.Discard, s)
		return err
	})
	laptop := f.client("laptop")
	f.authorize(laptop, "laptop")
	c, err := f.dial(laptop)
	must(t, err)
	ctx := context.Background()
	a, err := c.Open(ctx, "attach", map[string]string{"Session": "s1"}, nil)
	must(t, err)
	b, err := c.Open(ctx, "attach", map[string]string{"Session": "s2"}, nil)
	must(t, err)
	eventually(t, "the attaches", func() bool {
		cl := f.h.Clients()
		return len(cl[0].Connections) == 1 && strings.Join(cl[0].Connections[0].Sessions, ",") == "s1,s2"
	})

	// Turning Remote off on s1 closes its stream.
	if n := f.h.CloseSession("s1"); n != 1 {
		t.Errorf("closed %d", n)
	}
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := a.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("s1's stream: %v", err)
	}
	<-released
	eventually(t, "the release", func() bool { return strings.Join(f.h.Clients()[0].Connections[0].Sessions, ",") == "s2" })
	_ = b.Close()

	// On a connection that has gone, an attach is closed at once.
	closed := &closer{}
	f.h.Attach(&wire.Conn{}, "s3", closed)()
	if !closed.closed {
		t.Error("not closed")
	}
}

type closer struct{ closed bool }

func (c *closer) Close() error { c.closed = true; return nil }

func TestWrongHostFingerprint(t *testing.T) {
	f := newFixture(t, true)
	laptop := f.client("laptop")
	f.authorize(laptop, "laptop")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := wire.Dial(ctx, f.addr(), laptop, f.client("impostor").Fingerprint(), wire.Hello{})
	var fe *wire.FingerprintError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v", err)
	}
	eventually(t, "the log", func() bool { return strings.Contains(f.logs.String(), "handshake with 127.0.0.1:") })
}

func TestHostingOnOff(t *testing.T) {
	f := newFixture(t, false)
	st, err := f.h.SetHosting(true)
	must(t, err)
	if !st.Enabled || !st.Listening {
		t.Fatalf("on: %+v", st)
	}
	laptop := f.client("laptop")
	f.authorize(laptop, "laptop")
	c, err := f.dial(laptop)
	must(t, err)
	st, err = f.h.SetHosting(false)
	must(t, err)
	if st.Enabled || st.Listening {
		t.Errorf("off: %+v", st)
	}
	waitDone(t, c, "Hosting off")
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(f.port)); err == nil {
		t.Error("still listening")
	}
	cfg, err := config.Load(f.config)
	must(t, err)
	if cfg.Remote.Host.Enabled {
		t.Error("config.toml still says on")
	}
	// Listening on all interfaces.
	f.set("remote.host", "listen", "all")
	f.set("remote.host", "enabled", true)
	must(t, f.h.Reload())
	if st := f.h.Status(); st.Address != "0.0.0.0:"+strconv.Itoa(f.port) || st.Listen != "all" {
		t.Errorf("all: %+v", st)
	}
}

func TestPortFallback(t *testing.T) {
	f := newFixture(t, false)
	occupy(t, f.port)
	st, err := f.h.SetHosting(true)
	must(t, err)
	if !st.Listening || st.Port == f.port || st.ConfiguredPort != f.port || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	picked := st.Port
	if !strings.Contains(f.logs.String(), "port "+strconv.Itoa(f.port)+" is in use: using "+strconv.Itoa(picked)+" instead") {
		t.Errorf("logs:\n%s", f.logs)
	}
	b, err := os.ReadFile(f.state)
	must(t, err)
	if !strings.Contains(string(b), `"port": `+strconv.Itoa(picked)) {
		t.Errorf("state:\n%s", b)
	}
	// A client reaches it there.
	laptop := f.client("laptop")
	f.authorize(laptop, "laptop")
	_, err = f.dial(laptop)
	must(t, err)

	// Next time, it's the same port.
	f.h.Close()
	f.h = f.newHost()
	if st := f.h.Status(); st.Port != picked {
		t.Errorf("restarted on %d, not %d", st.Port, picked)
	}
	if !strings.Contains(f.logs.String(), "using "+strconv.Itoa(picked)+", as last time") {
		t.Errorf("logs:\n%s", f.logs)
	}
	// Unless that one's taken too.
	f.h.Close()
	occupy(t, picked)
	f.h = f.newHost()
	if st := f.h.Status(); !st.Listening || st.Port == picked || st.Port == f.port {
		t.Errorf("status = %+v", st)
	}
}

func TestOwnPortTaken(t *testing.T) {
	f := newFixture(t, false)
	f.set("remote.host", "port_fallback", false)
	free := occupy(t, f.port)
	st, err := f.h.SetHosting(true)
	must(t, err)
	want := "port " + strconv.Itoa(f.port) + " is in use, so Hosting can't start"
	if st.Listening || !strings.HasPrefix(st.Error, want) || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	if !strings.Contains(f.logs.String(), want) {
		t.Errorf("logs:\n%s", f.logs)
	}
	// Once it's free, a reload tries again.
	free()
	must(t, f.h.Reload())
	if st := f.h.Status(); !st.Listening || st.Port != f.port || st.Error != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestHostingErrors(t *testing.T) {
	f := newFixture(t, false)
	f.set("remote.host", "cert", "/nowhere/cert.pem")
	_, err := f.h.SetHosting(true)
	must(t, err)
	if st := f.h.Status(); st.Listening || !strings.Contains(st.Error, "identity: set both cert and key") {
		t.Errorf("half a cert: %+v", st)
	}
	f.set("remote.host", "cert", "")
	f.set("remote.host", "port", 0)
	must(t, f.h.Reload())
	if st := f.h.Status(); st.Listening || !strings.Contains(st.Error, "remote.host.port is from 1 to 65535") {
		t.Errorf("port 0: %+v", st)
	}
	f.set("remote.host", "port", f.port)
	f.h.o.Listen = func(string) (net.Listener, error) { return nil, errors.New("no network") }
	must(t, f.h.Reload())
	if st := f.h.Status(); !strings.Contains(st.Error, "can't listen on 127.0.0.1:"+strconv.Itoa(f.port)+": no network") {
		t.Errorf("no network: %+v", st)
	}
}

func TestReloadIfChanged(t *testing.T) {
	f := newFixture(t, true)
	must(t, f.h.ReloadIfChanged()) // nothing changed
	before := f.h.Status().Since
	f.clock.Add(time.Hour)
	must(t, f.h.ReloadIfChanged())
	if !f.h.Status().Since.Equal(before) {
		t.Error("restarted without a change")
	}
	// A change on disk is picked up; one that doesn't touch Hosting keeps it running.
	info, _ := os.Stat(f.config)
	f.set("remote", "new_sessions_remote", true)
	must(t, os.Chtimes(f.config, time.Now(), info.ModTime().Add(time.Second)))
	must(t, f.h.ReloadIfChanged())
	if !f.h.Status().Since.Equal(before) {
		t.Error("restarted for a change elsewhere")
	}
	f.set("remote.host", "enabled", false)
	must(t, os.Chtimes(f.config, time.Now(), info.ModTime().Add(2*time.Second)))
	must(t, f.h.ReloadIfChanged())
	if f.h.Status().Listening {
		t.Error("still on")
	}
}

func TestRequestLimits(t *testing.T) {
	f := newFixture(t, true)
	h := f.h
	peer := func(i int) wire.Peer {
		// Fingerprints only need to differ.
		fp := identity.Fingerprint([]byte{byte(i), byte(i >> 8)})
		return wire.Peer{Fingerprint: fp, CommonName: "m" + strconv.Itoa(i), Addr: "10.0.0.1:1"}
	}
	record := func(i int) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, _, err := h.recordLocked(peer(i))
		return err
	}
	for i := range MaxNewRequests {
		must(t, record(i))
	}
	// Too many new ones at once.
	if err := record(100); !errors.Is(err, errTooManyRequests) {
		t.Fatalf("over the rate: %v", err)
	}
	if v := h.authorize(peer(101)); v.Status != wire.StatusUnauthorized || !strings.Contains(v.Message, "too many requests") {
		t.Errorf("verdict = %+v", v)
	}
	// Repeats still count.
	must(t, record(0))
	if r := h.Requests()[0]; r.Count != 2 {
		t.Errorf("repeat: %+v", r)
	}
	// A minute on, new ones are taken again, up to MaxRequests.
	n := MaxNewRequests
	for n < MaxRequests {
		f.clock.Add(RequestWindow)
		for range MaxNewRequests {
			must(t, record(n))
			n++
		}
	}
	f.clock.Add(RequestWindow)
	if err := record(n); !errors.Is(err, errTooManyRequests) {
		t.Errorf("over MaxRequests: %v", err)
	}
	// They expire a day after their client last tried.
	f.clock.Add(RequestTTL - 5*RequestWindow)
	must(t, record(n-1)) // keeps the newest alive
	f.clock.Add(5 * RequestWindow)
	reqs := h.Requests()
	if len(reqs) != 1 || reqs[0].Fingerprint != peer(n-1).Fingerprint {
		t.Errorf("after a day: %d requests", len(reqs))
	}
	if st := h.Status(); st.Pending != 1 {
		t.Errorf("status = %+v", st)
	}
	// The state file followed.
	st, err := loadState(f.state)
	must(t, err)
	if len(st.Requests) != 1 {
		t.Errorf("state has %d requests", len(st.Requests))
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	must(t, os.WriteFile(bad, []byte("{"), 0o600))
	if _, err := loadState(bad); err == nil {
		t.Error("read a bad state file")
	}
	// A bad state file starts afresh, and says so.
	var l logs
	h := New(Options{StateFile: bad, Log: log.New(&l, "", 0)})
	h.Close()
	if !strings.Contains(l.String(), "starting afresh") {
		t.Errorf("logs: %s", l.String())
	}
	if _, err := loadState(dir); err == nil {
		t.Error("read a folder")
	}
	// A state file that can't be written is logged.
	h = New(Options{StateFile: filepath.Join(bad, "x", "state.json"), Log: log.New(&l, "", 0)})
	h.mu.Lock()
	h.saveLocked()
	h.mu.Unlock()
	h.Close()
	if !strings.Contains(l.String(), "remote: saving") {
		t.Errorf("logs: %s", l.String())
	}
	// No state file: nothing is saved.
	h = New(Options{})
	h.mu.Lock()
	h.saveLocked()
	h.mu.Unlock()
	h.Close()
}

func TestChanges(t *testing.T) {
	f := newFixture(t, true)
	f.mu.Lock()
	n := f.changes
	f.mu.Unlock()
	if n == 0 {
		t.Error("no change reported")
	}
}
