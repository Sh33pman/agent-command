package wire

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/remote/identity"
)

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Ensure(filepath.Join(t.TempDir(), "id"))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// server is a host on a loopback port that serves mux to whoever authorize
// lets in.
type server struct {
	addr  string
	id    *identity.Identity
	mux   *Mux
	errs  chan error
	peers chan Peer
}

func serve(t *testing.T, authorize func(Peer) Verdict) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{addr: ln.Addr().String(), id: newIdentity(t), mux: NewMux(), errs: make(chan error, 16), peers: make(chan Peer, 16)}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				c, err := Accept(ctx, raw, ServerConfig{Identity: s.id, Hello: Hello{Version: "1.0.0", Name: "host"}, Authorize: authorize})
				if err != nil {
					s.errs <- err
					return
				}
				s.peers <- c.Peer
				s.mux.Serve(ctx, c)
			})
		}
	})
	return s
}

func allow(Peer) Verdict { return Verdict{Status: StatusOK} }

func dial(t *testing.T, s *server, id *identity.Identity) (*Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, s.addr, id, s.id.Fingerprint(), Hello{Version: "1.0.1", Name: "laptop"})
	if c != nil {
		t.Cleanup(func() { _ = c.Close() })
	}
	return c, err
}

func TestCallAndStream(t *testing.T) {
	s := serve(t, allow)
	s.mux.Handle("add", func(_ context.Context, c *Call) (any, error) {
		var args struct{ A, B int }
		if err := c.Decode(&args); err != nil {
			return nil, err
		}
		return map[string]any{"sum": args.A + args.B, "from": c.Conn.Peer.Name}, nil
	})
	s.mux.Handle("nothing", func(context.Context, *Call) (any, error) { return nil, nil })
	s.mux.Handle("missing", func(context.Context, *Call) (any, error) {
		return nil, Errorf(CodeNotFound, "no session %q", "x")
	})
	s.mux.Handle("broken", func(context.Context, *Call) (any, error) { return nil, errors.New("disk full") })
	s.mux.Handle("panics", func(context.Context, *Call) (any, error) { panic("oops") })
	s.mux.HandleStream("echo", func(_ context.Context, c *Call, st *Stream) error {
		if err := st.OK(map[string]string{"op": c.Op}); err != nil {
			return err
		}
		_, err := io.Copy(st, st)
		return err
	})
	s.mux.HandleStream("events", func(_ context.Context, _ *Call, st *Stream) error {
		for i := range 3 {
			if err := st.Send(map[string]int{"n": i}); err != nil {
				return err
			}
		}
		return nil // answered OK for it, and closed
	})
	s.mux.HandleStream("refuse", func(context.Context, *Call, *Stream) error {
		return Errorf(CodeNotFound, "not remote")
	})
	s.mux.HandleStream("twice", func(_ context.Context, _ *Call, st *Stream) error {
		_ = st.OK(nil)
		return st.OK(nil)
	})

	me := newIdentity(t)
	c, err := dial(t, s, me)
	if err != nil {
		t.Fatal(err)
	}
	if c.Peer.Fingerprint != s.id.Fingerprint() || c.Peer.Name != "host" || c.Peer.Version != "1.0.0" || c.Peer.Proto != Proto {
		t.Errorf("client's peer = %+v", c.Peer)
	}
	p := <-s.peers
	if p.Fingerprint != me.Fingerprint() || p.CommonName != me.CommonName || p.Name != "laptop" || !strings.HasPrefix(p.Addr, "127.0.0.1:") {
		t.Errorf("host's peer = %+v", p)
	}
	ctx := context.Background()

	var sum struct {
		Sum  int
		From string
	}
	if err := c.Call(ctx, "add", map[string]int{"A": 2, "B": 3}, &sum); err != nil || sum.Sum != 5 || sum.From != "laptop" {
		t.Errorf("add = %+v, %v", sum, err)
	}
	if err := c.Call(ctx, "nothing", nil, &sum); err != nil {
		t.Errorf("nothing: %v", err)
	}
	if err := c.Call(ctx, "add", json.RawMessage(`"x"`), nil); !IsCode(err, CodeBadRequest) {
		t.Errorf("bad args: %v", err)
	}
	for op, code := range map[string]string{"missing": CodeNotFound, "broken": CodeInternal, "panics": CodeInternal, "nope": CodeUnknownOp, "refuse": CodeNotFound} {
		err := c.Call(ctx, op, nil, nil)
		if !IsCode(err, code) || !strings.HasPrefix(err.Error(), op+": ") {
			t.Errorf("%s: %v, want code %s", op, err, code)
		}
	}
	if err := c.Call(ctx, "add", func() {}, nil); err == nil {
		t.Error("args that can't be marshaled")
	}

	var opened map[string]string
	st, err := c.Open(ctx, "echo", nil, &opened)
	if err != nil || opened["op"] != "echo" {
		t.Fatalf("echo: %v %v", opened, err)
	}
	if err := st.Send("hello"); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := st.Receive(&got); err != nil || got != "hello" {
		t.Errorf("echoed %q, %v", got, err)
	}
	if _, err := st.Write([]byte("raw")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(st, buf); err != nil || string(buf) != "raw" {
		t.Errorf("raw echo %q, %v", buf, err)
	}
	_ = st.Close()

	ev, err := c.Open(ctx, "events", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		var e struct{ N int }
		if err := ev.Receive(&e); err != nil || e.N != i {
			t.Errorf("event %d = %+v, %v", i, e, err)
		}
	}
	if err := ev.Receive(new(any)); !errors.Is(err, io.EOF) {
		t.Errorf("after the last event: %v", err)
	}
	if err := c.Call(ctx, "twice", nil, nil); err != nil {
		t.Errorf("twice: %v", err)
	}

	if ops := s.mux.Ops(); strings.Join(ops, ",") != "add,broken,echo,events,missing,nothing,panics,refuse,twice" {
		t.Errorf("ops = %v", ops)
	}
	// An op can move from a call to a stream.
	s.mux.HandleStream("add", func(context.Context, *Call, *Stream) error { return nil })
	s.mux.Handle("echo", func(context.Context, *Call) (any, error) { return "call", nil })
	var echo string
	if err := c.Call(ctx, "echo", nil, &echo); err != nil || echo != "call" {
		t.Errorf("echo as a call: %q %v", echo, err)
	}
	if err := c.Call(ctx, "echo", nil, new(int)); err == nil || !strings.Contains(err.Error(), "bad reply") {
		t.Errorf("a reply that doesn't decode: %v", err)
	}

	// Closing the connection ends it.
	_ = c.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done wasn't closed")
	}
	if err := c.Call(ctx, "add", nil, nil); err == nil {
		t.Error("a call on a closed connection")
	}
}

func TestStreamEndsWithConnection(t *testing.T) {
	s := serve(t, allow)
	ended := make(chan error, 1)
	s.mux.HandleStream("hold", func(ctx context.Context, _ *Call, st *Stream) error {
		_ = st.OK(nil)
		<-ctx.Done()
		_, err := st.Read(make([]byte, 1))
		ended <- err
		return nil
	})
	c, err := dial(t, s, newIdentity(t))
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Open(context.Background(), "hold", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if _, err := st.Read(make([]byte, 1)); err == nil {
		t.Error("read on a stream of a closed connection")
	}
	select {
	case err := <-ended:
		if err == nil {
			t.Error("the host's stream is still open")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler's context didn't end with the connection")
	}
}

func TestOpenCanceled(t *testing.T) {
	s := serve(t, allow)
	s.mux.Handle("slow", func(ctx context.Context, _ *Call) (any, error) {
		<-ctx.Done()
		return nil, nil
	})
	c, err := dial(t, s, newIdentity(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("slow: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		verdict Verdict
		want    error
		text    string
	}{
		{Verdict{Status: StatusPending, Message: "request abc"}, ErrPending, "waiting for the host to approve this client: request abc"},
		{Verdict{Status: StatusPending}, ErrPending, "waiting for the host to approve this client"},
		{Verdict{Status: StatusUnauthorized, Message: "too many requests"}, ErrUnauthorized, "the host refused this client: too many requests"},
		{Verdict{Status: "weird"}, ErrUnauthorized, "the host refused this client"},
	} {
		s := serve(t, func(Peer) Verdict { return tc.verdict })
		_, err := dial(t, s, newIdentity(t))
		if !errors.Is(err, tc.want) || err.Error() != tc.text {
			t.Errorf("%+v: client got %v", tc.verdict, err)
		}
		serr := <-s.errs
		var pe *PeerError
		if !errors.Is(serr, tc.want) || !errors.As(serr, &pe) || pe.Peer.Name != "laptop" {
			t.Errorf("%+v: host got %v", tc.verdict, serr)
		}
	}
}

func TestWrongHost(t *testing.T) {
	s := serve(t, allow)
	other := newIdentity(t).Fingerprint()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Dial(ctx, s.addr, newIdentity(t), other, Hello{})
	var fe *FingerprintError
	if !errors.As(err, &fe) || fe.Want != other || fe.Got != s.id.Fingerprint() {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "the host's fingerprint is "+s.id.Fingerprint()+", not "+other) {
		t.Errorf("message: %v", err)
	}
	if serr := <-s.errs; serr == nil {
		t.Error("the host accepted a client that hung up")
	}
	if _, err := Dial(ctx, "127.0.0.1:1", newIdentity(t), other, Hello{}); err == nil {
		t.Error("dialed a closed port")
	}
}

// loopback is the two ends of a TCP connection: net.Pipe doesn't buffer, so
// both ends of TLS writing at once would wait on each other.
func loopback(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	b, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

func TestVersionMismatch(t *testing.T) {
	// A client of another protocol: the host says so, naming both.
	host := newIdentity(t)
	a, b := loopback(t)
	defer a.Close()
	got := make(chan error, 1)
	go func() {
		_, err := Accept(context.Background(), b, ServerConfig{Identity: host, Hello: Hello{Version: "0.60.0", Name: "studio"}, Authorize: allow})
		got <- err
	}()
	tc := tls.Client(a, ClientTLS(newIdentity(t), host.Fingerprint()))
	if err := writeLine(tc, Hello{Proto: Proto + 1, Version: "0.70.0", Name: "laptop"}); err != nil {
		t.Fatal(err)
	}
	var w Welcome
	if err := readLine(bufio.NewReader(tc), &w); err != nil {
		t.Fatal(err)
	}
	if w.Status != StatusVersion || w.Proto != Proto || !strings.Contains(w.Message, "laptop speaks Remote protocol 2 (acmd 0.70.0), this acmd speaks 1 (acmd 0.60.0)") {
		t.Errorf("welcome = %+v", w)
	}
	var ve *VersionError
	if err := <-got; !errors.As(err, &ve) || ve.Theirs.Proto != Proto+1 {
		t.Errorf("host: %v", err)
	}

}

func TestClientSeesOtherProtocol(t *testing.T) {
	for _, status := range []string{StatusOK, StatusVersion} {
		host := newIdentity(t)
		a, b := loopback(t)
		go func() {
			tc := tls.Server(b, ServerTLS(host))
			var h Hello
			if readLine(bufio.NewReader(tc), &h) == nil {
				_ = writeLine(tc, Welcome{Hello: Hello{Proto: Proto + 1, Version: "0.70.0", Name: "studio"}, Status: status})
			}
			_ = tc.Close()
		}()
		_, err := Handshake(context.Background(), a, newIdentity(t), host.Fingerprint(), Hello{Version: "0.58.0"})
		var ve *VersionError
		if !errors.As(err, &ve) || err.Error() != "protocol mismatch: studio speaks Remote protocol 2 (acmd 0.70.0), this acmd speaks 1 (acmd 0.58.0): update the older one" {
			t.Errorf("%s: %v", status, err)
		}
	}
	if got := (&VersionError{}).Error(); !strings.Contains(got, "the other side speaks Remote protocol 0 (acmd unknown)") {
		t.Errorf("empty VersionError: %s", got)
	}
}

func TestOddHosts(t *testing.T) {
	for name, odd := range map[string]struct {
		answer func(*tls.Conn)
		want   string
	}{
		"hangs up": {func(tc *tls.Conn) { _ = tc.Close() }, "closed the connection during the hello"},
		"garbage":  {func(tc *tls.Conn) { _, _ = tc.Write([]byte("nope\n")) }, "reading the host's hello"},
		"status": {func(tc *tls.Conn) {
			_ = writeLine(tc, Welcome{Hello: Hello{Proto: Proto}, Status: "maybe", Message: "hm"})
		}, `the host answered "maybe": hm`},
	} {
		host := newIdentity(t)
		a, b := loopback(t)
		go func() {
			tc := tls.Server(b, ServerTLS(host))
			var h Hello
			if readLine(bufio.NewReader(tc), &h) == nil {
				odd.answer(tc)
			}
			_ = tc.Close()
		}()
		_, err := Handshake(context.Background(), a, newIdentity(t), host.Fingerprint(), Hello{})
		if err == nil || !strings.Contains(err.Error(), odd.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
