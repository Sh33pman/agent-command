package term

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	initialRows, initialCols = 24, 80

	clientWriteTimeout = 2 * time.Second
	replayWriteTimeout = 10 * time.Second
)

// Server runs one session's shell.
type Server struct {
	Socket string   // unix socket to listen on
	Shell  []string // argv of the shell, absolute path first
	Dir    string   // its working folder
	Env    []string // its environment
	Keep   int      // output to keep for replay (0 = Keep)
	// Ready is called once the socket listens; Ended once the shell is
	// gone (both may be nil).
	Ready, Ended func()
}

// Serve runs the shell until it exits, the terminal is ended ('k'), or
// ctx ends (then the shell is hung up too). It removes its socket.
func (s *Server) Serve(ctx context.Context) error {
	if s.Keep == 0 {
		s.Keep = Keep
	}
	_ = os.Remove(s.Socket) // stale: the caller made sure no server runs
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return err
	}
	defer os.Remove(s.Socket)
	defer ln.Close()

	cmd := exec.Command(s.Shell[0], s.Shell[1:]...) // #nosec G204 -- the user's own shell
	cmd.Dir, cmd.Env = s.Dir, s.Env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: initialRows, Cols: initialCols})
	if err != nil {
		return err
	}
	defer ptmx.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	var hangupOnce sync.Once
	hangup := func() {
		hangupOnce.Do(func() {
			if cmd.Process == nil {
				return
			}
			pgid := -cmd.Process.Pid
			_ = syscall.Kill(pgid, syscall.SIGHUP) // its process group (pty.Start sets setsid)
			go func() {
				select {
				case <-exited:
				case <-time.After(500 * time.Millisecond):
					_ = syscall.Kill(pgid, syscall.SIGTERM)
					select {
					case <-exited:
					case <-time.After(500 * time.Millisecond):
						_ = syscall.Kill(pgid, syscall.SIGKILL)
					}
				}
			}()
		})
	}
	if s.Ready != nil {
		s.Ready()
	}

	// Answers to the program's terminal queries go through a goroutine: the
	// PTY can block while the program waits for us to read its output.
	answers := make(chan []byte, 64)
	go func() {
		for {
			select {
			case b := <-answers:
				_, _ = ptmx.Write(b)
			case <-exited:
				return
			}
		}
	}()

	st := &serverState{
		ptmx:    ptmx,
		clients: map[net.Conn]struct{}{},
		size:    pty.Winsize{Rows: initialRows, Cols: initialCols},
	}
	st.screen = newScreen(initialRows, initialCols, s.Keep, func(b []byte) {
		// While attached, the client's terminal answers. Called with mu held.
		if len(st.clients) > 0 {
			return
		}
		select {
		case answers <- b:
		default:
		}
	})

	output := make(chan struct{})
	go func() { // the shell's output: keep it, pass it on
		defer close(output)
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				st.output(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	attaches := make(chan attachRequest)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { // only an attach becomes a client
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				typ, p, err := readFrame(c)
				_ = c.SetReadDeadline(time.Time{})
				switch {
				case err == nil && typ == frameAttach:
					attaches <- attachRequest{conn: c, rows: attachSize(p, 2), cols: attachSize(p, 4)}
				case err == nil && typ == frameWrite:
					_, _ = ptmx.Write(p)
					_ = c.Close()
				case err == nil && typ == frameText:
					_, _ = c.Write([]byte(st.text()))
					_ = c.Close()
				case err == nil && typ == frameFront:
					if f, ok := front(ptmx, cmd.Process.Pid); ok {
						b, _ := json.Marshal(f)
						_, _ = c.Write(b)
					}
					_ = c.Close()
				case err == nil && typ == frameKill:
					hangup()
					_ = c.Close()
				default:
					_ = c.Close() // a probe
				}
			}()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			hangup()
			<-exited
			return s.end(st)
		case <-exited:
			<-output
			return s.end(st)
		case a := <-attaches:
			st.attach(a)
			go st.frames(a.conn, hangup)
		}
	}
}

func (s *Server) end(st *serverState) error {
	st.close()
	if s.Ended != nil {
		s.Ended()
	}
	return nil
}

// attachRequest is a client asking for the output, with its size when known.
type attachRequest struct {
	conn       net.Conn
	rows, cols uint16
}

// serverState is what the server's goroutines share.
type serverState struct {
	ptmx *os.File

	mu      sync.Mutex
	screen  screen
	clients map[net.Conn]struct{}
	size    pty.Winsize
	closed  bool
}

func (st *serverState) output(p []byte) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	st.screen.Write(p)
	for c := range st.clients {
		_ = c.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
		if _, err := c.Write(p); err != nil {
			_ = c.Close() // too slow or gone: it can attach again
			delete(st.clients, c)
		}
	}
}

// attach sends the client the current state and adds it, under one lock, so it
// neither misses nor repeats output.
func (st *serverState) attach(a attachRequest) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		_ = a.conn.Close()
		return
	}
	if a.rows > 0 && a.cols > 0 {
		st.resize(pty.Winsize{Rows: a.rows, Cols: a.cols})
	}
	_ = a.conn.SetWriteDeadline(time.Now().Add(replayWriteTimeout))
	if _, err := a.conn.Write(st.screen.Replay()); err != nil {
		_ = a.conn.Close()
		return
	}
	st.clients[a.conn] = struct{}{}
}

// resize applies the size last asked for: with several clients, the last one
// to resize decides. Call with mu held.
func (st *serverState) resize(ws pty.Winsize) {
	if ws == st.size {
		if st.screen.Exact() || ws.Rows <= 1 {
			return
		}
		// Same size (another session's pane): nudge so full-screen programs
		// redraw for a client that can't get the exact state.
		nudge := ws
		nudge.Rows--
		_ = pty.Setsize(st.ptmx, &nudge)
	}
	st.screen.Resize(int(ws.Rows), int(ws.Cols))
	_ = pty.Setsize(st.ptmx, &ws)
	st.size = ws
}

func (st *serverState) text() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ""
	}
	return st.screen.Text()
}

func (st *serverState) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	for c := range st.clients {
		_ = c.Close()
		delete(st.clients, c)
	}
	if !st.closed {
		st.closed = true
		st.screen.Close()
	}
}

// frames handles one client's input, resizes and kill.
func (st *serverState) frames(c net.Conn, hangup func()) {
	defer func() {
		st.mu.Lock()
		delete(st.clients, c)
		st.mu.Unlock()
		_ = c.Close()
	}()
	for {
		typ, p, err := readFrame(c)
		if err != nil {
			return
		}
		switch typ {
		case frameInput:
			_, _ = st.ptmx.Write(p)
		case frameResize:
			if len(p) != 4 {
				continue
			}
			st.mu.Lock()
			if !st.closed {
				st.resize(pty.Winsize{Rows: binary.BigEndian.Uint16(p), Cols: binary.BigEndian.Uint16(p[2:])})
			}
			st.mu.Unlock()
		case frameKill:
			hangup()
			return
		}
	}
}

// Running reports whether a server answers on socket.
func Running(socket string) bool {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Kill ends the terminal on socket (its shell is hung up).
func Kill(socket string) error {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	return writeFrame(c, frameKill, nil)
}

// Send types data into the shell on socket without attaching, so nobody
// watching sees a client come or go. The reply popup uses it to answer a
// session.
func Send(socket string, data []byte) error {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return ErrEnded
	}
	defer c.Close()
	return writeFrame(c, frameWrite, data)
}

// ScreenText returns the terminal's current screen and history as plain text.
func ScreenText(socket string) (string, error) {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return "", ErrEnded
	}
	defer c.Close()
	if err := writeFrame(c, frameText, nil); err != nil {
		return "", err
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(c)
	return string(b), err
}

// Foreground returns the program in front of the terminal on socket. ok is
// false when the server can't tell, or is older than this question.
func Foreground(socket string) (f Front, ok bool) {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return Front{}, false
	}
	defer c.Close()
	if err := writeFrame(c, frameFront, nil); err != nil {
		return Front{}, false
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b, err := io.ReadAll(c)
	if err != nil || len(b) == 0 || json.Unmarshal(b, &f) != nil {
		return Front{}, false
	}
	return f, f.Shell || f.Name != ""
}
