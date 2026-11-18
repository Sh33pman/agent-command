package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Call is one stream's op and arguments, on the side that serves it.
type Call struct {
	Op   string
	Args json.RawMessage
	Conn *Conn // the connection it came in on: Conn.Peer is who asked
}

// Decode reads the arguments into v. Bad arguments are a CodeBadRequest error.
func (c *Call) Decode(v any) error {
	if len(c.Args) == 0 {
		return nil
	}
	if err := json.Unmarshal(c.Args, v); err != nil {
		return Errorf(CodeBadRequest, "bad arguments: %v", err)
	}
	return nil
}

// Handler serves a call: what it returns is the reply's data, or its error
// (a *CallError keeps its code; anything else is CodeInternal).
type Handler func(ctx context.Context, c *Call) (any, error)

// StreamHandler serves a long-lived stream. It calls s.OK once the op has
// started, then uses s until it's done; the stream is closed when it returns.
// An error before s.OK is the reply. ctx ends with the connection.
type StreamHandler func(ctx context.Context, c *Call, s *Stream) error

// Mux serves ops by name. Handlers can be added while it serves.
type Mux struct {
	mu      sync.RWMutex
	calls   map[string]Handler
	streams map[string]StreamHandler
}

// NewMux is a Mux with no ops.
func NewMux() *Mux {
	return &Mux{calls: map[string]Handler{}, streams: map[string]StreamHandler{}}
}

// Handle serves op as a call, replacing what served it before.
func (m *Mux) Handle(op string, h Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streams, op)
	m.calls[op] = h
}

// HandleStream serves op as a long-lived stream, replacing what served it
// before.
func (m *Mux) HandleStream(op string, h StreamHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.calls, op)
	m.streams[op] = h
}

// Ops are the ops served, sorted.
func (m *Mux) Ops() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ops []string
	for op := range m.calls {
		ops = append(ops, op)
	}
	for op := range m.streams {
		ops = append(ops, op)
	}
	slices.Sort(ops)
	return ops
}

// headerTimeout is how long a new stream has to name its op.
var headerTimeout = 10 * time.Second

// Serve answers the streams the peer opens on c until c ends or ctx does,
// then waits for its handlers to return.
func (m *Mux) Serve(ctx context.Context, c *Conn) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	var wg sync.WaitGroup
	for {
		ys, err := c.sess.AcceptStream()
		if err != nil {
			break
		}
		wg.Go(func() { m.serveStream(ctx, c, ys) })
	}
	stop()
	cancel()
	_ = c.Close()
	wg.Wait()
}

func (m *Mux) serveStream(ctx context.Context, c *Conn, ys *yamux.Stream) {
	s := newStream(ys)
	defer s.Close()
	var h Header
	_ = ys.SetDeadline(time.Now().Add(headerTimeout))
	if err := readLine(s.r, &h); err != nil {
		return
	}
	_ = ys.SetDeadline(time.Time{})
	call := &Call{Op: h.Op, Args: h.Args, Conn: c}
	m.mu.RLock()
	handler, stream := m.calls[h.Op], m.streams[h.Op]
	m.mu.RUnlock()
	var data any
	var err error
	switch {
	case handler != nil:
		data, err = protect(func() (any, error) { return handler(ctx, call) })
	case stream != nil:
		_, err = protect(func() (any, error) { return nil, stream(ctx, call, s) })
	default:
		err = Errorf(CodeUnknownOp, "unknown op %q", h.Op)
	}
	if err != nil {
		_ = s.reply(errorReply(err))
		return
	}
	_ = s.OK(data)
}

// protect turns a handler's panic into an error, so one bad call doesn't take
// the daemon down.
func protect(fn func() (any, error)) (data any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	return fn()
}

func errorReply(err error) Reply {
	var ce *CallError
	if errors.As(err, &ce) {
		code := ce.Code
		if code == "" {
			code = CodeInternal
		}
		return Reply{Code: code, Error: ce.Message}
	}
	return Reply{Code: CodeInternal, Error: err.Error()}
}
