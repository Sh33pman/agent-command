// Package client is the client side of Remote sessions: the daemon's
// connections to the Remote hosts it knows ([[remote.hosts]] in config.toml),
// the sessions added here from them, and a local socket for each of those
// that relays to the host. The apps and the CLI attach to that socket as to a
// local session's, and never touch the network themselves.
//
// A host is connected while it has added sessions, or while something uses
// it (browsing, the Where picker), and for IdleAfter after. A connection that
// drops is retried with a backoff and jitter; a host whose fingerprint isn't
// the pinned one is never retried until its entry changes.
//
// The daemon keeps the added sessions (Backend): this package tells it what
// the hosts say about them. Their inbox items are kept here, in memory: the
// host sends them afresh on every connection.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/peer"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Added is a session added here from a Remote host: ID is the local
// session's, RemoteID its id on the host.
type Added struct {
	ID       string
	Host     string
	RemoteID string
	Archived bool // ended here: no socket, and it doesn't keep its host connected
}

// Update is what a host says about an added session.
type Update struct {
	Session api.Session
	// Unseen, when set, is the session's new unseen flag: the host's changed
	// since the last update.
	Unseen *bool
}

// Backend is the daemon, which keeps the added sessions.
type Backend interface {
	// Notice is a notification a host decided on for one of its sessions.
	Notice(session, host string, n ipc.Notice)
	// Changed says something the apps show has changed: a host's tree or
	// state, or an inbox.
	Changed()
}

// Options configure a Client.
type Options struct {
	// Settings is [remote] as it's in effect, from config.toml: its hosts
	// (read-only here) and [remote.client]. nil is the defaults.
	Settings func() config.Remote
	// Data keeps the hosts added through ops (state.db). nil keeps them in
	// memory.
	Data Data
	// IdentityDir is acmd's generated identity, used unless [remote.client]
	// names a cert and key.
	IdentityDir string
	Version     string // this acmd's, for the hello
	Log         *log.Logger
	Now         func() time.Time
	// Socket is where an added session's local socket goes: the daemon puts
	// it where a local session's host listens, so the apps attach unchanged.
	Socket func(id string) string
	// Dial connects to a host (wire.Dial by default).
	Dial func(ctx context.Context, addr string, id *identity.Identity, pinned string, hello wire.Hello) (*wire.Conn, error)
	// CheckPeer checks who connected to a local socket (peer.Check: the same
	// user).
	CheckPeer func(net.Conn) error
	// MinBackoff and MaxBackoff bound the wait between tries (500 ms, 30 s).
	MinBackoff, MaxBackoff time.Duration
	// Jitter spreads a wait (default: a random part of its second half).
	Jitter func(time.Duration) time.Duration
	// After waits (time.After).
	After func(time.Duration) <-chan time.Time
	// IdleAfter is how long a host stays connected after it was last used,
	// when it has no added sessions (2 min).
	IdleAfter time.Duration
	// DialTimeout bounds a dial and its handshake (10 s).
	DialTimeout time.Duration
	// LostGrace is how long a relay that the host ended waits to learn
	// whether the connection dropped, and it should reattach (300 ms).
	LostGrace time.Duration
}

// Client is the client side of Remote sessions. Its methods are safe to call
// at once.
type Client struct {
	o      Options
	b      Backend
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	cfgMu sync.Mutex // changing the known hosts

	mu       sync.Mutex
	hosts    map[string]*hostConn // by name
	order    []string             // config.toml's, then the ones added through ops
	inConfig map[string]bool      // the hosts config.toml lists
	added    map[string]*added    // by local id
	id       *identity.Identity
	idErr    error
	idKey    [2]string // the cert and key it was resolved from
	started  bool
	closed   bool
}

// added is one of a host's sessions here, with its local socket.
type added struct {
	Added
	proxy *proxy
}

// New makes a Client. Start begins it.
func New(o Options, b Backend) *Client {
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Data == nil {
		o.Data = &MemoryData{}
	}
	if o.Dial == nil {
		o.Dial = wire.Dial
	}
	if o.CheckPeer == nil {
		o.CheckPeer = peer.Check
	}
	if o.MinBackoff == 0 {
		o.MinBackoff = 500 * time.Millisecond
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.Jitter == nil {
		o.Jitter = jitter
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.IdleAfter == 0 {
		o.IdleAfter = 2 * time.Minute
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.LostGrace == 0 {
		o.LostGrace = 300 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{o: o, b: b, ctx: ctx, cancel: cancel, hosts: map[string]*hostConn{}, added: map[string]*added{}}
}

// jitter is a random wait from half of d to d.
func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	return d/2 + rand.N(d/2) // #nosec G404 -- spreading retries needs no secret
}

// backoff is the wait before try n (from 1): doubling from min, up to max.
func backoff(n int, lo, hi time.Duration) time.Duration {
	d := lo
	for i := 1; i < n && d < hi; i++ {
		d *= 2
	}
	return min(d, hi)
}

// Start reads config.toml and the known hosts, and connects to them: each
// host's sessions get a local socket as its tree arrives.
func (c *Client) Start(context.Context) error {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	return c.Reload()
}

// Close disconnects every host, closes the sockets and waits for them.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	var proxies []*proxy
	for _, a := range c.added {
		if a.proxy != nil {
			proxies = append(proxies, a.proxy)
			a.proxy = nil
		}
	}
	for _, h := range c.hosts {
		h.stopLocked()
	}
	c.mu.Unlock()
	c.cancel()
	for _, p := range proxies {
		p.close()
	}
	c.wg.Wait()
}

// Reload reads the known hosts and [remote] again, and follows them: hosts
// added, edited or removed, and the client identity.
func (c *Client) Reload() error {
	if c == nil {
		return nil
	}
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.reloadLocked()
}

// reloadLocked follows the settings and the data (c.cfgMu is held).
func (c *Client) reloadLocked() error {
	r := config.DefaultRemote()
	if c.o.Settings != nil {
		r = c.o.Settings()
	}
	added, err := c.o.Data.KnownHosts()
	if err != nil {
		c.o.Log.Printf("remote hosts: %v (they stay as they were)", err)
		return err
	}
	inConfig := map[string]bool{}
	for _, h := range r.Hosts {
		inConfig[h.Name] = true
	}
	for _, h := range added {
		if !inConfig[h.Name] {
			r.Hosts = append(r.Hosts, h)
		}
	}
	c.mu.Lock()
	c.inConfig = inConfig
	c.mu.Unlock()
	c.apply(r)
	return nil
}

// apply follows r: hosts come and go, and an edited host reconnects.
func (c *Client) apply(r config.Remote) {
	key := [2]string{r.Client.Cert, r.Client.Key}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	newID := false
	if (c.id == nil && len(r.Hosts) > 0) || (c.id != nil && key != c.idKey) { // made only once a host needs it
		old := c.fingerprintLocked()
		c.id, c.idErr = identity.Resolve(r.Client.Cert, r.Client.Key, c.o.IdentityDir)
		c.idKey = key
		newID = c.fingerprintLocked() != old
		if c.idErr != nil {
			c.o.Log.Printf("remote hosts: the client identity: %v", c.idErr)
		}
	}
	seen := map[string]bool{}
	c.order = c.order[:0]
	for _, kh := range r.Hosts {
		if kh.Name == "" || seen[kh.Name] {
			continue
		}
		seen[kh.Name] = true
		c.order = append(c.order, kh.Name)
		h, ok := c.hosts[kh.Name]
		if !ok {
			h = newHostConn(c, kh)
			c.hosts[kh.Name] = h
			c.wg.Add(1)
			go h.run()
			continue
		}
		if h.cfg != kh || newID {
			h.cfg = kh
			h.redialLocked()
		}
	}
	for name, h := range c.hosts {
		if !seen[name] {
			h.stopLocked()
			delete(c.hosts, name)
		}
	}
	c.changedLocked()
}

func (c *Client) fingerprintLocked() string {
	if c.id == nil {
		return ""
	}
	return c.id.Fingerprint()
}

// changedLocked tells the daemon, without waiting for it (c.mu is held).
func (c *Client) changedLocked() {
	if c.started {
		go c.b.Changed()
	}
}

// Hosts are the known hosts, in config.toml's order, and their connections.
func (c *Client) Hosts() []ipc.RemoteHost {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ipc.RemoteHost, 0, len(c.order))
	for _, name := range c.order {
		if h := c.hosts[name]; h != nil {
			out = append(out, h.statusLocked())
		}
	}
	return out
}

// Host is one known host and its connection.
func (c *Client) Host(name string) (ipc.RemoteHost, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.hostLocked(name)
	if err != nil {
		return ipc.RemoteHost{}, err
	}
	return h.statusLocked(), nil
}

func (c *Client) hostLocked(name string) (*hostConn, error) {
	h := c.hosts[name]
	if h == nil {
		return nil, fmt.Errorf("there's no Remote host named %q", name)
	}
	return h, nil
}

// Retry connects to a host now, without waiting out the backoff. A host
// whose fingerprint didn't match is tried again too.
func (c *Client) Retry(name string) (ipc.RemoteHost, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.hostLocked(name)
	if err != nil {
		return ipc.RemoteHost{}, err
	}
	h.blocked = false
	h.leaseLocked()
	return h.statusLocked(), nil
}

// connect is a host's connection, once it's connected: it asks for one, and
// waits until it's there, the host refuses, or ctx ends.
func (c *Client) connect(ctx context.Context, name string) (*wire.Conn, error) {
	c.mu.Lock()
	h, err := c.hostLocked(name)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	h.leaseLocked()
	tries := h.tries
	for {
		switch {
		case h.state == ipc.HostConnected && h.conn != nil:
			conn := h.conn
			c.mu.Unlock()
			return conn, nil
		case h.blocked, h.tries > tries && failed(h.state), h.stopped:
			err := h.errorLocked()
			c.mu.Unlock()
			return nil, err
		}
		ch := h.changed
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			c.mu.Lock()
			err := fmt.Errorf("%s: %w", h.cfg.Name, ctx.Err())
			if h.err != "" {
				err = h.errorLocked()
			}
			c.mu.Unlock()
			return nil, err
		}
		c.mu.Lock()
	}
}

// failed says whether a state is a try that didn't connect.
func failed(state string) bool {
	switch state {
	case ipc.HostIdle, ipc.HostConnecting, ipc.HostConnected:
		return false
	}
	return true
}

// call runs op on a host, connecting to it first.
func (c *Client) call(ctx context.Context, host, op string, args, reply any) error {
	conn, err := c.connect(ctx, host)
	if err != nil {
		return err
	}
	if err := conn.Call(ctx, op, args, reply); err != nil {
		return hostError(host, err)
	}
	return nil
}

// hostError names the host in an error from it.
func hostError(host string, err error) error {
	var ce *wire.CallError
	if errors.As(err, &ce) {
		return &wire.CallError{Op: host, Code: ce.Code, Message: ce.Message}
	}
	return fmt.Errorf("%s: %w", host, err)
}

// identity is the client identity, or why there's none.
func (c *Client) identity() (*identity.Identity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id == nil && c.idErr == nil {
		c.idErr = errors.New("no client identity yet")
	}
	return c.id, c.idErr
}

// sortedAdded is the added sessions of host (all hosts when ""), by local id.
func (c *Client) sortedAddedLocked(host string) []*added {
	var out []*added
	for _, a := range c.added {
		if host == "" || a.Host == host {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y *added) int {
		switch {
		case x.ID < y.ID:
			return -1
		case x.ID > y.ID:
			return 1
		}
		return 0
	})
	return out
}

// needs connects to a host and checks it serves op: an older host that
// doesn't is not_supported, naming its acmd.
func (c *Client) needs(ctx context.Context, host, op string) error {
	if _, err := c.connect(ctx, host); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.hostLocked(host)
	if err != nil {
		return err
	}
	if len(h.ops) > 0 && !slices.Contains(h.ops, op) {
		return &wire.CallError{Op: host, Code: ipc.CodeNotSupported,
			Message: fmt.Sprintf("%s's acmd (%s) can't do that yet: update it", host, cmpOr(h.peer.Version, "older"))}
	}
	return nil
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
