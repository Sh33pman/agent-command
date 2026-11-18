package hostapi

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Attach relays a client's stream to the session's terminal byte for byte:
// after the OK reply, whatever the client writes goes to a fresh connection to
// the session's HostSocket, and whatever the terminal writes comes back. The
// host doesn't read term's frames; the client speaks term's protocol as a
// local pane does.
//
// The terminal drops a pane that stops reading, and a remote one reads over
// the network, so the relay reads the terminal at once into a queue of its
// own: a slow link doesn't slow the session down. When the queue overflows,
// or the client takes nothing for relayWriteTimeout, the relay ends; the
// client attaches again and gets the screen as it is then.

var (
	// relayQueue is how much output may wait for a client.
	relayQueue = 4 << 20
	// relayWriteTimeout is how long a client may take nothing.
	relayWriteTimeout = 30 * time.Second
	// dialTimeout bounds connecting to a session's terminal.
	dialTimeout = 2 * time.Second
)

func (a *API) attach(ctx context.Context, c *wire.Call, s *wire.Stream) error {
	var ref api.SessionRef
	if err := c.Decode(&ref); err != nil {
		return err
	}
	if _, _, err := a.find(ctx, ref.ID); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", a.b.HostSocket(ref.ID), dialTimeout)
	if err != nil {
		return wire.Errorf(api.CodeNotRunning, "the session's terminal has ended: resume it on the host first")
	}
	r := &relay{stream: s, conn: conn, done: make(chan struct{})}
	release := a.r.Attach(c.Conn, ref.ID, r)
	defer release()
	if err := s.OK(nil); err != nil {
		_ = r.Close()
		return nil
	}
	r.wg.Go(func() { a.watch(ctx, ref.ID, r) })
	r.run()
	return nil
}

// watch ends the relay when the session stops being visible: Remote turned
// off, archived or gone. (Turning Remote off also closes it through Hosting
// straight away.)
func (a *API) watch(ctx context.Context, id string, r *relay) {
	for {
		_, next := a.b.Changes()
		if _, _, err := a.find(ctx, id); wire.IsCode(err, wire.CodeNotFound) {
			_ = r.Close()
			return
		}
		select {
		case <-next:
		case <-r.done:
			return
		case <-ctx.Done():
			_ = r.Close()
			return
		}
	}
}

// relay is one attach: a client's stream and a connection to the terminal.
type relay struct {
	stream *wire.Stream
	conn   net.Conn
	once   sync.Once
	done   chan struct{}
	wg     sync.WaitGroup
}

// Close ends the relay: both ends close. Hosting calls it to drop a client.
func (r *relay) Close() error {
	r.once.Do(func() {
		close(r.done)
		_ = r.conn.Close()
		_ = r.stream.Close()
	})
	return nil
}

// run relays until either side ends, then waits for its goroutines.
func (r *relay) run() {
	q := &queue{ready: make(chan struct{}, 1)}
	// The client's frames, to the terminal.
	r.wg.Go(func() {
		_, _ = io.Copy(r.conn, r.stream)
		_ = r.Close()
	})
	// The terminal's output, into the queue at once.
	r.wg.Go(func() {
		defer q.end()
		for {
			b := make([]byte, 32<<10)
			n, err := r.conn.Read(b)
			if n > 0 && !q.push(b[:n]) {
				_ = r.Close() // the client is too far behind
				return
			}
			if err != nil {
				return
			}
		}
	})
	// The queue, to the client. What the terminal wrote last is sent before
	// the stream closes.
	r.wg.Go(func() {
		defer r.Close()
		for {
			chunks, ended := q.take()
			for _, b := range chunks {
				_ = r.stream.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
				if _, err := r.stream.Write(b); err != nil {
					return
				}
			}
			if ended {
				return
			}
			if len(chunks) == 0 {
				select {
				case <-q.ready:
				case <-r.done:
					return
				}
			}
		}
	})
	r.wg.Wait()
}

// queue is output waiting for a client, at most relayQueue bytes.
type queue struct {
	mu     sync.Mutex
	chunks [][]byte
	size   int
	ended  bool
	ready  chan struct{} // there's something to take
}

// push adds b, or says the queue is full.
func (q *queue) push(b []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.size+len(b) > relayQueue {
		return false
	}
	q.chunks, q.size = append(q.chunks, b), q.size+len(b)
	q.signal()
	return true
}

// end says nothing more comes.
func (q *queue) end() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ended = true
	q.signal()
}

func (q *queue) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// take is everything queued, and whether that's the last of it.
func (q *queue) take() ([][]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	chunks := q.chunks
	q.chunks, q.size = nil, 0
	return chunks, q.ended
}
