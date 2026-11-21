package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// hostConn is a known host and its connection. Its fields are guarded by the
// Client's mu.
type hostConn struct {
	c   *Client
	cfg config.KnownHost

	state, err, seen string
	since, retryAt   time.Time
	peer             wire.Peer
	conn             *wire.Conn
	tries            int  // dials that ended, connected or not
	failures         int  // in a row, for the backoff
	blocked          bool // a fingerprint mismatch: not tried again until the entry changes
	leaseUntil       time.Time
	stopped          bool
	// items are the open inbox items of each of the host's Remote sessions,
	// added or not, as its events last said: a session added later starts
	// with its own.
	items map[string][]api.InboxItem

	changed chan struct{} // closed, and replaced, on every change
	kick    chan struct{} // wakes run: something wants it, or the entry changed
	redial  bool          // the entry changed: drop the connection
	stop    chan struct{}
}

func newHostConn(c *Client, kh config.KnownHost) *hostConn {
	return &hostConn{c: c, cfg: kh, state: ipc.HostIdle, since: c.o.Now(), changed: make(chan struct{}), items: map[string][]api.InboxItem{},
		kick: make(chan struct{}, 1), stop: make(chan struct{})}
}

// The state changes and tells whoever waits (c.mu is held).
func (h *hostConn) setLocked(state, msg string, retryAt time.Time) {
	if state != h.state || msg != h.err {
		h.since = h.c.o.Now()
	}
	h.state, h.err, h.retryAt = state, msg, retryAt
	h.notifyLocked()
}

// notifyLocked wakes whoever waits on the host: its state, or its sessions,
// changed.
func (h *hostConn) notifyLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
	h.c.changedLocked()
}

func (h *hostConn) wake() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// leaseLocked keeps the host connected for IdleAfter, and connects it now.
func (h *hostConn) leaseLocked() {
	h.leaseUntil = h.c.o.Now().Add(h.c.o.IdleAfter)
	h.wake()
}

// redialLocked drops the connection for a changed entry, and tries again.
func (h *hostConn) redialLocked() {
	h.blocked, h.redial, h.failures, h.seen = false, true, 0, ""
	h.wake()
}

func (h *hostConn) stopLocked() {
	if !h.stopped {
		h.stopped = true
		close(h.stop)
		h.notifyLocked()
	}
}

// wantedLocked says whether the host should be connected: it has added
// sessions that aren't archived, or it's been used lately.
func (h *hostConn) wantedLocked() bool {
	if h.c.o.Now().Before(h.leaseUntil) {
		return true
	}
	for _, a := range h.c.added {
		if a.Host == h.cfg.Name && !a.Archived {
			return true
		}
	}
	return false
}

func (h *hostConn) statusLocked() ipc.RemoteHost {
	st := ipc.RemoteHost{Name: h.cfg.Name, Address: h.cfg.Address, Fingerprint: h.cfg.Fingerprint, Cert: h.cfg.Cert,
		State: h.state, Error: h.err, Since: h.since, RetryAt: h.retryAt, OurFingerprint: h.c.fingerprintLocked(),
		Seen: h.seen, InConfig: h.c.inConfig[h.cfg.Name]}
	if h.state == ipc.HostConnected {
		st.HostName, st.HostVersion = h.peer.Name, h.peer.Version
	}
	for _, a := range h.c.added {
		if a.Host == h.cfg.Name && !a.Archived {
			st.Sessions++
		}
	}
	return st
}

// errorLocked is why the host isn't connected.
func (h *hostConn) errorLocked() error {
	switch {
	case h.stopped:
		return fmt.Errorf("there's no Remote host named %q", h.cfg.Name)
	case h.err != "":
		return fmt.Errorf("%s: %s", h.cfg.Name, h.err)
	}
	return fmt.Errorf("%s isn't connected (%s)", h.cfg.Name, h.state)
}

// run keeps the host connected while it's wanted, until it's stopped.
func (h *hostConn) run() {
	c := h.c
	defer c.wg.Done()
	for {
		c.mu.Lock()
		if h.stopped {
			c.mu.Unlock()
			return
		}
		h.redial = false
		wanted, blocked := h.wantedLocked(), h.blocked
		if !wanted && !blocked && h.state != ipc.HostIdle {
			h.failures = 0
			h.setLocked(ipc.HostIdle, "", time.Time{})
		}
		if !wanted || blocked {
			c.mu.Unlock()
			select {
			case <-h.kick:
			case <-h.stop:
				return
			}
			continue
		}
		h.setLocked(ipc.HostConnecting, "", time.Time{})
		kh := h.cfg
		c.mu.Unlock()

		conn, err := h.dial(kh)

		c.mu.Lock()
		h.tries++
		if h.stopped || h.redial {
			c.mu.Unlock()
			if conn != nil {
				_ = conn.Close()
			}
			continue
		}
		if err != nil {
			state, msg := h.classifyLocked(err)
			h.failures++
			var wait time.Duration
			switch state {
			case ipc.HostFingerprint:
				h.blocked = true // never retried until the entry changes
			case ipc.HostUnauthorized, ipc.HostVersion, ipc.HostError:
				wait = c.o.MaxBackoff
			default:
				wait = backoff(h.failures, c.o.MinBackoff, c.o.MaxBackoff)
			}
			wait = c.o.Jitter(wait)
			var at time.Time
			if !h.blocked {
				at = c.o.Now().Add(wait)
			}
			h.setLocked(state, msg, at)
			c.o.Log.Printf("remote host %s: %s", kh.Name, msg)
			blocked := h.blocked // read under the lock: Retry clears it
			c.mu.Unlock()
			if blocked {
				continue
			}
			select {
			case <-c.o.After(wait):
			case <-h.kick:
			case <-h.stop:
				return
			}
			continue
		}
		h.failures = 0
		h.conn, h.peer = conn, conn.Peer
		h.setLocked(ipc.HostConnected, "", time.Time{})
		c.o.Log.Printf("remote host %s: connected to %s (acmd %s)", kh.Name, kh.Address, conn.Peer.Version)
		c.mu.Unlock()

		h.serve(conn)

		c.mu.Lock()
		h.conn = nil
		if !h.stopped && !h.redial && h.state == ipc.HostConnected {
			h.failures = 1
			h.setLocked(ipc.HostUnreachable, "the connection dropped", time.Time{})
		}
		c.mu.Unlock()
	}
}

// dial connects to the host as the client identity, and checks it speaks
// the host API.
func (h *hostConn) dial(kh config.KnownHost) (*wire.Conn, error) {
	c := h.c
	id, err := c.identity()
	if err != nil {
		return nil, &localError{fmt.Errorf("the client identity: %w", err)}
	}
	pinned := kh.Fingerprint
	if pinned == "" && kh.Cert != "" {
		if pinned, err = identity.FingerprintFromPEMFile(kh.Cert); err != nil {
			return nil, &localError{fmt.Errorf("the host's cert: %w", err)}
		}
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.o.DialTimeout)
	defer cancel()
	go func() {
		select {
		case <-h.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	conn, err := c.o.Dial(ctx, kh.Address, id, pinned, wire.Hello{Version: c.o.Version, Name: id.CommonName})
	if err != nil {
		return nil, err
	}
	var info wire.Info
	if err := conn.Call(ctx, wire.OpInfo, nil, &info); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if info.API != api.Version {
		_ = conn.Close()
		return nil, &apiError{theirs: info.API, version: info.Version}
	}
	return conn, nil
}

// localError is a problem on this side: the identity, or the host's cert.
type localError struct{ err error }

func (e *localError) Error() string { return e.err.Error() }
func (e *localError) Unwrap() error { return e.err }

// apiError is a host whose API isn't this client's.
type apiError struct {
	theirs  int
	version string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("the host's API is version %d (acmd %s), this acmd's is %d: update the older one", e.theirs, e.version, api.Version)
}

// classifyLocked is the state a failed dial leaves the host in, and why.
func (h *hostConn) classifyLocked(err error) (state, msg string) {
	var fe *wire.FingerprintError
	var ve *wire.VersionError
	var ae *apiError
	var le *localError
	switch {
	case errors.As(err, &fe):
		h.seen = fe.Got
		return ipc.HostFingerprint, fe.Error() + ". Check the fingerprint, and edit the host to trust it"
	case errors.Is(err, wire.ErrPending):
		return ipc.HostPending, fmt.Sprintf("waiting for %s to approve this client: its fingerprint is %s", h.cfg.Name, h.c.fingerprintLocked())
	case errors.Is(err, wire.ErrUnauthorized):
		return ipc.HostUnauthorized, err.Error()
	case errors.As(err, &ve), errors.As(err, &ae):
		return ipc.HostVersion, err.Error()
	case errors.As(err, &le):
		return ipc.HostError, err.Error()
	}
	return ipc.HostUnreachable, err.Error()
}

// serve follows the host's events while the connection lasts, and the host
// is wanted. It returns once the connection is closed.
func (h *hostConn) serve(conn *wire.Conn) {
	c := h.c
	defer func() { _ = conn.Close() }()
	go h.follow(conn)
	for {
		select {
		case <-conn.Done():
			return
		case <-h.stop:
			return
		case <-h.kick:
			c.mu.Lock()
			redial := h.redial
			c.mu.Unlock()
			if redial {
				return
			}
		case <-c.o.After(c.o.IdleAfter):
			c.mu.Lock()
			wanted := h.wantedLocked()
			c.mu.Unlock()
			if !wanted {
				return
			}
		}
	}
}

// follow applies the host's events until the stream ends, which ends the
// connection: the next one starts with a snapshot.
func (h *hostConn) follow(conn *wire.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(h.c.ctx, h.c.o.DialTimeout)
	s, err := conn.Open(ctx, api.OpEvents, nil, nil)
	cancel()
	if err != nil {
		h.c.o.Log.Printf("remote host %s: events: %v", h.cfg.Name, err)
		return
	}
	defer func() { _ = s.Close() }()
	for {
		var ev api.Event
		if err := s.Receive(&ev); err != nil {
			return
		}
		h.c.applyEvent(h, ev)
	}
}
