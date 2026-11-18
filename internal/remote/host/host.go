// Package host is Hosting: the daemon's TCP listener that lets other acmds
// (clients) use this machine's Remote sessions.
//
// It follows [remote] in config.toml: [remote.host] turns it on and off and
// says where it listens, and [[remote.authorized_clients]] are the clients it
// lets in, known by their cert's fingerprint (internal/remote/wire has the
// protocol). A client it doesn't know is refused, and its request is kept for
// the user to approve or deny; approving adds it to config.toml.
//
// What a client may do is up to the ops registered with Handle and
// HandleStream; the host itself serves only wire.OpPing and wire.OpInfo.
package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Options configure a Host.
type Options struct {
	// ConfigFile is config.toml: Reload reads [remote] from it, and approving,
	// authorizing, revoking and turning Hosting on or off write to it. ""
	// leaves Hosting off, and those fail.
	ConfigFile string
	// StateFile keeps what isn't config: pending requests, and the port the
	// fallback picked last.
	StateFile string
	// IdentityDir is acmd's generated identity, used unless [remote.host]
	// names a cert and key.
	IdentityDir string
	Version     string // this acmd's, for the hello
	Log         *log.Logger
	Now         func() time.Time
	// OnChange is called after anything the apps show changes: Hosting's
	// status, connections, attaches or requests.
	OnChange func()
	// Listen opens a TCP listener (net.Listen by default).
	Listen func(addr string) (net.Listener, error)
}

// Host is Hosting. Its methods are safe to call at once.
type Host struct {
	o      Options
	mux    *wire.Mux
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{} // handshakes under way

	cfgMu      sync.Mutex // config.toml: reading it and writing to it
	configRead time.Time  // its modification time when last read
	read       bool

	mu          sync.Mutex
	remote      config.Remote // as last applied
	key         listenKey     // what the listener was started with
	id          *identity.Identity
	ln          net.Listener
	since       time.Time
	err         string
	conns       map[*wire.Conn]*conn
	connSeq     int
	attachments map[*attachment]struct{}
	st          state
	newRequests []time.Time // when the last new requests came, for the rate limit
	closed      bool
}

// listenKey is what the listener depends on: when it changes, it restarts.
type listenKey struct {
	enabled     bool
	listen      string
	port        int
	fallback    bool
	fingerprint string
}

// conn is a connected client.
type conn struct {
	id    string
	c     *wire.Conn
	name  string
	since time.Time
}

// attachment is a client's stream attached to a session.
type attachment struct {
	conn    *conn
	session string
	stream  io.Closer
}

// maxHandshakes is how many connections may be in TLS or the hello at once;
// more are closed straight away.
const maxHandshakes = 32

// New makes a Host, with Hosting off until Apply or Reload turns it on.
func New(o Options) *Host {
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Listen == nil {
		o.Listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{o: o, mux: wire.NewMux(), ctx: ctx, cancel: cancel, sem: make(chan struct{}, maxHandshakes),
		remote: config.DefaultRemote(), conns: map[*wire.Conn]*conn{}, attachments: map[*attachment]struct{}{}}
	st, err := loadState(o.StateFile)
	if err != nil {
		o.Log.Printf("remote: %v (starting afresh)", err)
	}
	h.st = st
	h.mux.Handle(wire.OpPing, func(context.Context, *wire.Call) (any, error) { return nil, nil })
	h.Handle(wire.OpInfo, h.info)
	return h
}

// Handle serves op to authorized clients as a call. Calls are logged with
// who made them.
func (h *Host) Handle(op string, fn wire.Handler) {
	h.mux.Handle(op, func(ctx context.Context, c *wire.Call) (any, error) {
		h.o.Log.Printf("remote: %s from %s", op, h.who(c.Conn))
		return fn(ctx, c)
	})
}

// HandleStream serves op to authorized clients as a long-lived stream, such
// as events or a session's terminal.
func (h *Host) HandleStream(op string, fn wire.StreamHandler) {
	h.mux.HandleStream(op, func(ctx context.Context, c *wire.Call, s *wire.Stream) error {
		h.o.Log.Printf("remote: %s from %s", op, h.who(c.Conn))
		return fn(ctx, c, s)
	})
}

func (h *Host) info(context.Context, *wire.Call) (any, error) {
	h.mu.Lock()
	id := h.id
	h.mu.Unlock()
	if id == nil {
		return nil, errors.New("hosting is off")
	}
	return wire.Info{Name: id.CommonName, Fingerprint: id.Fingerprint(), Version: h.o.Version, Proto: wire.Proto,
		API: api.Version, Ops: h.mux.Ops()}, nil
}

// Close stops Hosting, ends every connection and waits for them.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	h.stopLocked()
	h.mu.Unlock()
	h.cancel()
	h.wg.Wait()
}

// Reload reads [remote] from config.toml and applies it. A config.toml that
// doesn't parse leaves Hosting as it is.
func (h *Host) Reload() error {
	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()
	return h.reloadLocked(true)
}

// ReloadIfChanged is Reload when config.toml has changed since it was last
// read.
func (h *Host) ReloadIfChanged() error {
	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()
	return h.reloadLocked(false)
}

func (h *Host) reloadLocked(force bool) error {
	if h.o.ConfigFile == "" {
		return nil
	}
	var mod time.Time
	if info, err := os.Stat(h.o.ConfigFile); err == nil {
		mod = info.ModTime()
	}
	if !force && h.read && mod.Equal(h.configRead) {
		return nil
	}
	h.configRead, h.read = mod, true
	c, err := config.Load(h.o.ConfigFile)
	if err != nil {
		h.o.Log.Printf("remote: %v (Hosting stays as it was)", err)
		return err
	}
	h.Apply(c.Remote)
	return nil
}

// Apply follows r: it starts, stops or restarts the listener when [remote.host]
// or the identity changed, and drops clients that are no longer authorized.
func (h *Host) Apply(r config.Remote) {
	var id *identity.Identity
	var idErr error
	if r.Host.Enabled {
		id, idErr = identity.Resolve(r.Host.Cert, r.Host.Key, h.o.IdentityDir)
	}
	key := listenKey{enabled: r.Host.Enabled, listen: r.Host.Listen, port: r.Host.Port, fallback: r.Host.PortFallback}
	if id != nil {
		key.fingerprint = id.Fingerprint()
	}

	h.mu.Lock()
	h.remote = r
	h.closeUnauthorizedLocked()
	if h.closed || (key == h.key && h.err == "") {
		h.mu.Unlock()
		h.changed()
		return
	}
	h.stopLocked()
	h.key, h.id = key, id
	switch {
	case !r.Host.Enabled:
		h.o.Log.Printf("remote: Hosting is off")
	case idErr != nil:
		h.err = "identity: " + idErr.Error()
	default:
		if err := (config.Remote{Host: r.Host}).Validate(); err != nil {
			h.err = err.Error()
			break
		}
		ln, err := h.bindLocked(r.Host)
		if err != nil {
			h.err = err.Error()
			break
		}
		h.ln, h.since = ln, h.o.Now()
		h.o.Log.Printf("remote: Hosting on %s as %s (%s)", ln.Addr(), id.Fingerprint(), id.CommonName)
		h.wg.Go(func() { h.acceptLoop(ln, id) })
	}
	if h.err != "" {
		h.o.Log.Printf("remote: Hosting can't start: %s", h.err)
	}
	h.mu.Unlock()
	h.changed()
}

// stopLocked closes the listener and every connection (h.mu is held).
func (h *Host) stopLocked() {
	if h.ln != nil {
		_ = h.ln.Close()
		h.o.Log.Printf("remote: stopped listening on %s", h.ln.Addr())
		h.ln = nil
	}
	for c := range h.conns {
		_ = c.Close()
	}
	h.since, h.err = time.Time{}, ""
}

// bindLocked opens the listener for hc. When its port is taken and the
// fallback is on, it tries the port the fallback picked last time for the
// same settings, then any free port, and remembers the one it gets.
func (h *Host) bindLocked(hc config.RemoteHost) (net.Listener, error) {
	addr := hc.BindAddress()
	ln, err := h.o.Listen(addr)
	if err == nil {
		return ln, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, fmt.Errorf("can't listen on %s: %w", addr, err)
	}
	if !hc.PortFallback {
		return nil, fmt.Errorf("port %d is in use, so Hosting can't start: free it, choose another port, or turn on port_fallback", hc.Port)
	}
	ip, _, _ := net.SplitHostPort(addr)
	if p := h.st.Port; p != nil && p.Listen == hc.Listen && p.Configured == hc.Port && p.Port != hc.Port {
		if ln, err := h.o.Listen(net.JoinHostPort(ip, strconv.Itoa(p.Port))); err == nil {
			h.o.Log.Printf("remote: port %d is in use: using %d, as last time", hc.Port, p.Port)
			return ln, nil
		}
	}
	ln, err = h.o.Listen(net.JoinHostPort(ip, "0"))
	if err != nil {
		return nil, fmt.Errorf("port %d is in use, and no other port could be opened: %w", hc.Port, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	h.st.Port = &rememberedPort{Listen: hc.Listen, Configured: hc.Port, Port: port}
	h.saveLocked()
	h.o.Log.Printf("remote: port %d is in use: using %d instead", hc.Port, port)
	return ln, nil
}

func (h *Host) acceptLoop(ln net.Listener, id *identity.Identity) {
	for {
		raw, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			h.o.Log.Printf("remote: accept: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		select {
		case h.sem <- struct{}{}:
		default:
			h.o.Log.Printf("remote: too many connections at once: closed one from %s", raw.RemoteAddr())
			_ = raw.Close()
			continue
		}
		h.wg.Go(func() { h.serve(raw, ln, id) })
	}
}

func (h *Host) serve(raw net.Conn, ln net.Listener, id *identity.Identity) {
	c, err := wire.Accept(h.ctx, raw, wire.ServerConfig{
		Identity:  id,
		Hello:     wire.Hello{Version: h.o.Version, Name: id.CommonName},
		Authorize: h.authorize,
	})
	<-h.sem
	if err != nil {
		h.logRefusal(raw.RemoteAddr().String(), err)
		return
	}
	cn := h.register(c, ln)
	if cn == nil {
		_ = c.Close()
		return
	}
	h.o.Log.Printf("remote: %s connected (acmd %s)", h.who(c), c.Peer.Version)
	h.changed()
	h.mux.Serve(h.ctx, c)
	h.unregister(cn)
	h.o.Log.Printf("remote: %s disconnected", h.who(c))
	h.changed()
}

func (h *Host) logRefusal(addr string, err error) {
	var pe *wire.PeerError
	switch {
	case errors.Is(err, wire.ErrPending), errors.Is(err, wire.ErrUnauthorized):
		// authorize logged it.
	case errors.As(err, &pe):
		h.o.Log.Printf("remote: refused %s (%q) from %s: %v", pe.Peer.Fingerprint, pe.Peer.CommonName, pe.Peer.Addr, err)
	default:
		h.o.Log.Printf("remote: handshake with %s failed: %v", addr, err)
	}
}

// register adds a connection that got in, unless it came through a listener
// that has since stopped, or its client was revoked meanwhile.
func (h *Host) register(c *wire.Conn, ln net.Listener) *conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	ac, ok := h.authorizedLocked(c.Peer.Fingerprint)
	if h.ln != ln || !ok {
		return nil
	}
	h.connSeq++
	cn := &conn{id: "c" + strconv.Itoa(h.connSeq), c: c, name: ac.Name, since: h.o.Now()}
	h.conns[c] = cn
	return cn
}

func (h *Host) unregister(cn *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, cn.c)
	for a := range h.attachments {
		if a.conn == cn {
			delete(h.attachments, a)
		}
	}
}

// who names a connection's client for the log: its name, fingerprint and
// address.
func (h *Host) who(c *wire.Conn) string {
	h.mu.Lock()
	name := ""
	if cn := h.conns[c]; cn != nil {
		name = cn.name
	} else if ac, ok := h.authorizedLocked(c.Peer.Fingerprint); ok {
		name = ac.Name
	}
	h.mu.Unlock()
	return fmt.Sprintf("%q (%s) at %s", name, c.Peer.Fingerprint, c.Peer.Addr)
}

func (h *Host) changed() {
	if h.o.OnChange != nil {
		h.o.OnChange()
	}
}

// Status is Hosting's status.
func (h *Host) Status() ipc.RemoteStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked(h.o.Now())
	hc := h.remote.Host
	s := ipc.RemoteStatus{Enabled: hc.Enabled, Listen: hc.Listen, ConfiguredPort: hc.Port, PortFallback: hc.PortFallback,
		Error: h.err, Proto: wire.Proto, Authorized: len(h.remote.AuthorizedClients), Connected: len(h.conns), Pending: len(h.st.Requests)}
	if h.ln != nil {
		s.Listening, s.Since = true, h.since
		if a, ok := h.ln.Addr().(*net.TCPAddr); ok {
			s.Port = a.Port
		}
		// As configured: Go shows 0.0.0.0 as [::], as it listens on IPv6 too.
		ip, _, _ := net.SplitHostPort(hc.BindAddress())
		s.Address = net.JoinHostPort(ip, strconv.Itoa(s.Port))
	}
	if h.id != nil {
		s.Fingerprint, s.Name, s.Cert = h.id.Fingerprint(), h.id.CommonName, h.id.CertPath
	}
	return s
}

// Attach records that the client on c is attached to a session through
// stream, until release is called; the attach shows in Clients. CloseSession
// closes the stream. On a connection that has already gone, it closes stream
// at once.
func (h *Host) Attach(c *wire.Conn, session string, stream io.Closer) (release func()) {
	h.mu.Lock()
	cn := h.conns[c]
	if cn == nil {
		h.mu.Unlock()
		_ = stream.Close()
		return func() {}
	}
	a := &attachment{conn: cn, session: session, stream: stream}
	h.attachments[a] = struct{}{}
	h.mu.Unlock()
	h.changed()
	return sync.OnceFunc(func() {
		h.mu.Lock()
		delete(h.attachments, a)
		h.mu.Unlock()
		h.changed()
	})
}

// CloseSession closes every client's stream attached to session, as when it
// stops being Remote, and says how many it closed.
func (h *Host) CloseSession(session string) int {
	h.mu.Lock()
	var streams []io.Closer
	for a := range h.attachments {
		if a.session == session {
			streams = append(streams, a.stream)
		}
	}
	h.mu.Unlock()
	for _, s := range streams {
		_ = s.Close()
	}
	return len(streams)
}

// connectionsLocked are the connections, oldest first (h.mu is held).
func (h *Host) connectionsLocked() []ipc.RemoteConnection {
	var out []ipc.RemoteConnection
	for _, cn := range h.conns {
		rc := ipc.RemoteConnection{ID: cn.id, Name: cn.name, Fingerprint: cn.c.Peer.Fingerprint, CommonName: cn.c.Peer.CommonName,
			Addr: cn.c.Peer.Addr, Version: cn.c.Peer.Version, Since: cn.since}
		for a := range h.attachments {
			if a.conn == cn && !slices.Contains(rc.Sessions, a.session) {
				rc.Sessions = append(rc.Sessions, a.session)
			}
		}
		slices.Sort(rc.Sessions)
		out = append(out, rc)
	}
	// Ids count up: "c2" came before "c10".
	slices.SortFunc(out, func(a, b ipc.RemoteConnection) int {
		x, _ := strconv.Atoi(a.ID[1:])
		y, _ := strconv.Atoi(b.ID[1:])
		return x - y
	})
	return out
}
