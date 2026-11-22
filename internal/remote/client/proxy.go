package client

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/term"
)

// The local proxy: each added session has a unix socket (0600, the same user
// only) where a local session's host would listen. Each connection to it is
// relayed to the session's attach stream on its host: after the host's OK,
// that stream is a byte pipe to the session's terminal (internal/term), so
// term.Attach, `acmd host attach` and the apps work unchanged.
//
// The relay reads term's frames as they pass, to know an attach from a
// one-off (Send, ScreenText and the like) and to keep the pane's size. When
// the connection to the host drops, an attached pane stays: the relay waits
// for the host, attaches again at the pane's size, and the host's replay
// redraws it. Input typed meanwhile is dropped. While the host is offline, a
// new connection waits for it in the same way.

const (
	frameAttach = 'a'
	frameInput  = 'i'
	frameResize = 'r'
	maxFrame    = 1 << 20
)

type frame struct {
	typ     byte
	payload []byte
}

func readFrame(r io.Reader) (frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return frame{}, errors.New("frame too large")
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return frame{}, err
	}
	return frame{hdr[0], p}, nil
}

func (f frame) bytes() []byte {
	b := make([]byte, 5, 5+len(f.payload))
	b[0] = f.typ
	binary.BigEndian.PutUint32(b[1:], uint32(len(f.payload))) // #nosec G115 -- frames are at most maxFrame
	return append(b, f.payload...)
}

// proxy is an added session's local socket.
type proxy struct {
	c    *Client
	id   string // the local session
	sock string
	ln   net.Listener

	unlistenOnce sync.Once
	mu           sync.Mutex
	conns        map[net.Conn]struct{}
	closed       bool
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

// openLocked opens an added session's socket (c.mu is held). A socket that
// can't be opened is logged: the session stays, unattachable.
func (c *Client) openLocked(a *added) {
	if a.proxy != nil || c.o.Socket == nil || c.closed {
		return
	}
	p, err := c.listen(a.ID)
	if err != nil {
		c.o.Log.Printf("remote session %s: its socket: %v", a.ID, err)
		return
	}
	a.proxy = p
}

func (c *Client) listen(id string) (*proxy, error) {
	sock := c.o.Socket(id)
	_ = os.Remove(sock) // left by a daemon that crashed: the id is ours
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(c.ctx)
	p := &proxy{c: c, id: id, sock: sock, ln: ln, conns: map[net.Conn]struct{}{}, ctx: ctx, cancel: cancel}
	p.wg.Add(1)
	go p.accept()
	return p, nil
}

// closeProxyLocked takes an added session's socket away at once; its relays
// end without c.mu held.
func (c *Client) closeProxyLocked(a *added) {
	if p := a.proxy; p != nil {
		a.proxy = nil
		p.unlisten()
		go p.close()
	}
}

// unlisten stops taking connections, and takes the socket away.
func (p *proxy) unlisten() {
	p.unlistenOnce.Do(func() {
		_ = p.ln.Close()
		_ = os.Remove(p.sock)
	})
}

func (p *proxy) accept() {
	defer p.wg.Done()
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		if err := p.c.o.CheckPeer(conn); err != nil {
			p.c.o.Log.Printf("remote session %s: rejected a connection: %v", p.id, err)
			_ = conn.Close()
			continue
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = conn.Close()
			return
		}
		p.conns[conn] = struct{}{}
		p.wg.Add(1)
		p.mu.Unlock()
		go func() {
			defer p.wg.Done()
			p.serve(conn)
			p.mu.Lock()
			delete(p.conns, conn)
			p.mu.Unlock()
		}()
	}
}

// close stops the socket and ends its relays.
func (p *proxy) close() {
	p.mu.Lock()
	p.closed = true
	for conn := range p.conns {
		_ = conn.Close()
	}
	p.mu.Unlock()
	p.cancel()
	p.unlisten()
	p.wg.Wait()
}

// errEnded is a session whose terminal has ended on the host.
var errEnded = errors.New("the session's terminal has ended")

// open opens the session's attach stream, waiting for its host while it's
// offline, or while it doesn't offer the session. It gives up when done is
// closed, the socket closes, or the terminal has ended (errEnded).
func (p *proxy) open(done <-chan struct{}, drain <-chan frame) (*wire.Stream, *wire.Conn, error) {
	c := p.c
	for {
		c.mu.Lock()
		a := c.added[p.id]
		if a == nil {
			c.mu.Unlock()
			return nil, nil, errEnded
		}
		h := c.hosts[a.Host]
		var conn *wire.Conn
		var changed chan struct{}
		if h != nil {
			conn, changed = h.conn, h.changed
		}
		remoteID := a.RemoteID
		c.mu.Unlock()
		if conn != nil {
			ctx, cancel := context.WithTimeout(p.ctx, c.o.DialTimeout)
			s, err := conn.Open(ctx, api.OpSessionAttach, api.AttachRequest{ID: remoteID, Mode: term.ModeStream}, nil)
			cancel()
			switch {
			case err == nil:
				return s, conn, nil
			case wire.IsCode(err, api.CodeNotRunning):
				return nil, nil, errEnded
			}
			// Not offered (yet), or the connection broke: wait for news.
		}
		if changed == nil { // the host isn't known: wait for config.toml
			changed = make(chan struct{})
		}
		for waiting := true; waiting; {
			select {
			case <-changed:
				waiting = false
			case <-done:
				return nil, nil, errEnded
			case <-p.ctx.Done():
				return nil, nil, errEnded
			case _, ok := <-drain:
				if !ok {
					return nil, nil, errEnded
				}
			case <-c.o.After(time.Second): // in case a change came between the checks
				waiting = false
			}
		}
	}
}

// serve relays one local connection.
func (p *proxy) serve(local net.Conn) {
	defer func() { _ = local.Close() }()
	first, err := readFrame(local)
	if err != nil {
		return // a liveness check: connected, then closed
	}
	if first.typ != frameAttach {
		p.oneOff(local, first)
		return
	}
	// Frames from the pane, as they come; the latest size is kept.
	var sizeMu sync.Mutex
	attach := first.payload
	frames := make(chan frame, 64)
	go func() {
		defer close(frames)
		for {
			f, err := readFrame(local)
			if err != nil {
				return
			}
			if f.typ == frameResize && len(f.payload) >= 4 && len(attach) >= 6 {
				sizeMu.Lock()
				attach = append(attach[:2:2], f.payload[:4]...)
				sizeMu.Unlock()
			}
			select {
			case frames <- f:
			case <-p.ctx.Done():
				return
			}
		}
	}()
	for {
		s, conn, err := p.open(nil, frames)
		if err != nil {
			return
		}
		sizeMu.Lock()
		hello := frame{frameAttach, append([]byte(nil), attach...)}
		sizeMu.Unlock()
		if !p.pump(local, s, conn, hello, frames) {
			return
		}
	}
}

// pump relays an attach until one end goes. It reports whether the
// connection to the host dropped, so the pane should attach again.
func (p *proxy) pump(local net.Conn, s *wire.Stream, conn *wire.Conn, hello frame, frames <-chan frame) bool {
	defer func() { _ = s.Close() }()
	out := make(chan struct{})
	go func() {
		defer close(out)
		_, _ = io.Copy(local, s)
	}()
	if _, err := s.Write(hello.bytes()); err != nil {
		<-out
		return p.lost(conn)
	}
	for {
		select {
		case f, ok := <-frames:
			if !ok { // the pane went
				_ = s.Close()
				<-out
				return false
			}
			if _, err := s.Write(f.bytes()); err != nil {
				_ = s.Close()
				<-out
				return p.lost(conn)
			}
		case <-out:
			return p.lost(conn)
		case <-p.ctx.Done():
			return false
		}
	}
}

// lost says whether an attach ended because its connection dropped, not
// because the host ended it: the connection goes down along with it.
func (p *proxy) lost(conn *wire.Conn) bool {
	select {
	case <-conn.Done():
		return true
	case <-p.ctx.Done():
		return false
	case <-p.c.o.After(p.c.o.LostGrace):
		return false
	}
}

// oneOff relays a connection that doesn't attach: it sends one frame, and
// reads the answer, if any. It doesn't wait long for an offline host.
func (p *proxy) oneOff(local net.Conn, first frame) {
	done := make(chan struct{})
	timer := time.AfterFunc(p.c.o.DialTimeout, func() { close(done) })
	defer timer.Stop()
	s, _, err := p.open(done, nil)
	if err != nil {
		return
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Write(first.bytes()); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(s, local)
		_ = s.Close()
	}()
	_, _ = io.Copy(local, s)
}
