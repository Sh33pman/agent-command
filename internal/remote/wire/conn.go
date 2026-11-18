package wire

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// HandshakeTimeout bounds TLS and the hello, unless the context ends first.
var HandshakeTimeout = 10 * time.Second

// Peer is the other side of a connection.
type Peer struct {
	Hello              // what it said in its hello
	Fingerprint string // its cert's
	CommonName  string // its cert's subject: its machine's name, for a generated identity
	Addr        string // its address
}

// Conn is a connection after the hello: yamux over TLS.
type Conn struct {
	Peer Peer
	sess *yamux.Session
}

// Close ends the connection, and every stream on it.
func (c *Conn) Close() error { return c.sess.Close() }

// Done is closed once the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.sess.CloseChan() }

// Verdict is what the host makes of a client: Status is StatusOK,
// StatusPending or StatusUnauthorized, and Message, when set, tells the client
// more.
type Verdict struct {
	Status  string
	Message string
}

// ServerConfig is the host's side of a connection.
type ServerConfig struct {
	Identity *identity.Identity
	Hello    Hello // this host's (wire fills in Proto)
	// Authorize decides whether a client may in, once its cert and hello are
	// known.
	Authorize func(Peer) Verdict
}

// PeerError is a connection that failed after the TLS handshake, so the peer
// is known.
type PeerError struct {
	Peer Peer
	Err  error
}

func (e *PeerError) Error() string { return e.Err.Error() }
func (e *PeerError) Unwrap() error { return e.Err }

// Accept runs the host's side of a new connection: TLS, the client's hello,
// cfg.Authorize and the welcome. Anything but StatusOK closes raw and returns
// a *PeerError wrapping ErrPending, ErrUnauthorized or a *VersionError.
func Accept(ctx context.Context, raw net.Conn, cfg ServerConfig) (*Conn, error) {
	tc := tls.Server(raw, ServerTLS(cfg.Identity))
	c, err := accept(ctx, tc, raw.RemoteAddr().String(), cfg)
	if err != nil {
		_ = tc.Close()
	}
	return c, err
}

func accept(ctx context.Context, tc *tls.Conn, addr string, cfg ServerConfig) (*Conn, error) {
	defer deadline(ctx, tc)()
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	cert := tc.ConnectionState().PeerCertificates[0]
	peer := Peer{Fingerprint: identity.Fingerprint(cert.Raw), CommonName: cert.Subject.CommonName, Addr: addr}
	r := bufio.NewReader(tc)
	if err := readLine(r, &peer.Hello); err != nil {
		return nil, &PeerError{peer, fmt.Errorf("reading the hello: %w", err)}
	}
	me := cfg.Hello
	me.Proto = Proto
	v := cfg.Authorize(peer)
	w := Welcome{Hello: me, Status: v.Status, Message: v.Message}
	var refused error
	switch {
	case v.Status == StatusPending:
		refused = refusal(ErrPending, v.Message)
	case v.Status != StatusOK:
		w.Status = StatusUnauthorized
		refused = refusal(ErrUnauthorized, v.Message)
	case peer.Proto != Proto:
		w.Status = StatusVersion
		refused = &VersionError{Ours: me, Theirs: peer.Hello}
		w.Message = refused.Error()
	}
	if err := writeLine(tc, w); err != nil {
		return nil, &PeerError{peer, err}
	}
	if refused != nil {
		return nil, &PeerError{peer, refused}
	}
	sess, err := yamux.Server(&bufConn{Conn: tc, r: r}, muxConfig())
	if err != nil {
		return nil, &PeerError{peer, err}
	}
	return &Conn{Peer: peer, sess: sess}, nil
}

// Dial connects to a host at addr ("host:port") as id, accepting only a host
// whose cert has the pinned fingerprint. It fails with a *FingerprintError
// for another host, ErrPending while the host waits for the user to approve
// this client, ErrUnauthorized when it refuses, and a *VersionError when the
// two speak different protocols.
func Dial(ctx context.Context, addr string, id *identity.Identity, pinned string, me Hello) (*Conn, error) {
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return Handshake(ctx, raw, id, pinned, me)
}

// Handshake runs the client's side over a connection that's already open,
// as Dial does; raw is closed when it fails.
func Handshake(ctx context.Context, raw net.Conn, id *identity.Identity, pinned string, me Hello) (*Conn, error) {
	tc := tls.Client(raw, ClientTLS(id, pinned))
	c, err := handshake(ctx, tc, raw.RemoteAddr().String(), me)
	if err != nil {
		_ = tc.Close()
	}
	return c, err
}

func handshake(ctx context.Context, tc *tls.Conn, addr string, me Hello) (*Conn, error) {
	defer deadline(ctx, tc)()
	if err := tc.HandshakeContext(ctx); err != nil {
		var fe *FingerprintError
		if errors.As(err, &fe) {
			return nil, fe
		}
		return nil, err
	}
	me.Proto = Proto
	if err := writeLine(tc, me); err != nil {
		return nil, err
	}
	r := bufio.NewReader(tc)
	var w Welcome
	if err := readLine(r, &w); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the host closed the connection during the hello")
		}
		return nil, fmt.Errorf("reading the host's hello: %w", err)
	}
	switch {
	case w.Status == StatusPending:
		return nil, refusal(ErrPending, w.Message)
	case w.Status == StatusUnauthorized:
		return nil, refusal(ErrUnauthorized, w.Message)
	case w.Status == StatusVersion || w.Proto != Proto:
		return nil, &VersionError{Ours: me, Theirs: w.Hello}
	case w.Status != StatusOK:
		return nil, fmt.Errorf("the host answered %q: %s", w.Status, w.Message)
	}
	cert := tc.ConnectionState().PeerCertificates[0]
	sess, err := yamux.Client(&bufConn{Conn: tc, r: r}, muxConfig())
	if err != nil {
		return nil, err
	}
	peer := Peer{Hello: w.Hello, Fingerprint: identity.Fingerprint(cert.Raw), CommonName: cert.Subject.CommonName, Addr: addr}
	return &Conn{Peer: peer, sess: sess}, nil
}

func refusal(sentinel error, msg string) error {
	if msg == "" {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, msg)
}

// deadline bounds the handshake by HandshakeTimeout and ctx, and returns what
// lifts it.
func deadline(ctx context.Context, c net.Conn) func() {
	_ = c.SetDeadline(time.Now().Add(HandshakeTimeout))
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	return func() {
		stop()
		_ = c.SetDeadline(time.Time{})
	}
}

func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	c.KeepAliveInterval = 15 * time.Second
	c.ConnectionWriteTimeout = 10 * time.Second
	return c
}

// bufConn reads through r, which may hold bytes read past the hello.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// Call runs op on the peer with args, and decodes its data into reply (when
// not nil). A refusal is a *CallError.
func (c *Conn) Call(ctx context.Context, op string, args, reply any) error {
	s, err := c.Open(ctx, op, args, reply)
	if err != nil {
		return err
	}
	return s.Close()
}

// Open starts a long-lived stream for op: once the peer answers OK (its data
// decoded into reply, when not nil), the stream carries what op defines. The
// caller closes it. ctx bounds only the opening.
func (c *Conn) Open(ctx context.Context, op string, args, reply any) (*Stream, error) {
	raw, err := marshal(args)
	if err != nil {
		return nil, err
	}
	ys, err := c.sess.OpenStream()
	if err != nil {
		return nil, err
	}
	s := newStream(ys)
	stop := context.AfterFunc(ctx, s.abort)
	var r Reply
	err = writeLine(ys, Header{Op: op, Args: raw})
	if err == nil {
		err = readLine(s.r, &r)
	}
	if !stop() {
		err = ctx.Err() // it may have come in just after the reply
	}
	if err != nil {
		s.abort()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if !r.OK {
		_ = ys.Close()
		return nil, &CallError{Op: op, Code: r.Code, Message: r.Error}
	}
	if reply != nil && len(r.Data) > 0 {
		if err := json.Unmarshal(r.Data, reply); err != nil {
			_ = ys.Close()
			return nil, fmt.Errorf("%s: bad reply: %w", op, err)
		}
	}
	s.replied = true
	return s, nil
}

var errAnswered = errors.New("wire: the stream has already been answered")

// Stream is one stream on a connection. Past the reply, it's raw bytes
// (Read, Write) or lines of JSON (Send, Receive), as its op defines.
type Stream struct {
	s       *yamux.Stream
	r       *bufio.Reader
	wmu     sync.Mutex
	replied bool // the reply has gone (or, for the opener, arrived)
}

func newStream(s *yamux.Stream) *Stream {
	return &Stream{s: s, r: bufio.NewReaderSize(s, 32<<10)}
}

// OK answers the stream's header with data, on the side that serves it: the
// op has started, and the stream carries on. Returning from a StreamHandler
// without calling OK answers for it.
func (s *Stream) OK(data any) error {
	raw, err := marshal(data)
	if err != nil {
		return err
	}
	return s.reply(Reply{OK: true, Data: raw})
}

func (s *Stream) reply(r Reply) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.replied {
		return errAnswered
	}
	s.replied = true
	return writeLine(s.s, r)
}

// Read reads what the peer wrote. Once the peer closes its end, it returns
// io.EOF: a handler that only writes can read to notice the client going.
func (s *Stream) Read(p []byte) (int, error) { return s.r.Read(p) }

// Write writes raw bytes. On the side that serves the stream, the first write
// answers OK first.
func (s *Stream) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.answer(); err != nil {
		return 0, err
	}
	return s.s.Write(p)
}

// Send writes v as one line of JSON, answering OK first like Write.
func (s *Stream) Send(v any) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.answer(); err != nil {
		return err
	}
	return writeLine(s.s, v)
}

// answer sends the OK reply if nothing has answered yet (s.wmu is held).
func (s *Stream) answer() error {
	if s.replied {
		return nil
	}
	s.replied = true
	return writeLine(s.s, Reply{OK: true})
}

// Receive reads one line of JSON into v.
func (s *Stream) Receive(v any) error { return readLine(s.r, v) }

// Close ends the stream: the peer reads io.EOF, and reads here stop.
func (s *Stream) Close() error {
	err := s.s.Close()
	_ = s.s.SetReadDeadline(time.Unix(1, 0))
	return err
}

// abort ends the stream at once, unblocking a read in progress.
func (s *Stream) abort() {
	_ = s.s.SetDeadline(time.Unix(1, 0))
	_ = s.s.Close()
}

// SetDeadline bounds reads and writes on the stream.
func (s *Stream) SetDeadline(t time.Time) error { return s.s.SetDeadline(t) }
