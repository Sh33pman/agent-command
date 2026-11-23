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
	"time"

	"github.com/creack/pty"

	"github.com/brettmostert/agent-command/internal/peer"
)

const (
	initialRows, initialCols = 24, 80

	clientWriteTimeout = 2 * time.Second
	replayWriteTimeout = 10 * time.Second

	// clientQueue is how many chunks of output a client may fall behind by
	// before the program waits for it, as it would for a slow terminal.
	clientQueue = 64
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
	// Like the daemon's socket: the folder is 0700 already, and a 'w' frame
	// here types into the session's shell.
	if err := os.Chmod(s.Socket, 0o600); err != nil {
		return err
	}

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
	hangup := func() { hangupOnce.Do(func() { hangupGroup(cmd.Process, exited) }) }
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
		clients: map[net.Conn]*client{},
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
				if peer.Check(c) != nil { // another user
					_ = c.Close()
					return
				}
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				typ, p, err := readFrame(c)
				_ = c.SetReadDeadline(time.Time{})
				switch {
				case err == nil && typ == frameAttach && !HasMode(attachMode(p)):
					_ = c.Close() // a mode it doesn't have: the client asked first (CheckMode)
				case err == nil && typ == frameAttach:
					attaches <- attachRequest{conn: c, rows: attachSize(p, 2), cols: attachSize(p, 4)}
				case err == nil && typ == frameMode:
					b, _ := json.Marshal(modeReply{OK: HasMode(string(p))})
					_, _ = c.Write(b)
					_ = c.Close()
				case err == nil && typ == frameWrite:
					_, _ = ptmx.Write(p)
					_ = c.Close()
				case err == nil && typ == frameText:
					_, _ = c.Write([]byte(st.text()))
					_ = c.Close()
				case err == nil && typ == frameAgent:
					st.mu.Lock()
					b, _ := json.Marshal(st.agent)
					st.mu.Unlock()
					_, _ = c.Write(b)
					_ = c.Close()
				case err == nil && typ == frameFront:
					if f, ok := st.front(cmd.Process.Pid); ok {
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
	clients map[net.Conn]*client
	size    pty.Winsize
	closed  bool

	osc   oscAgentScanner
	agent AgentState // the agent's last OSC 7501 report
}

// client is an attached pane. Its output goes through a queue and a goroutine of
// its own, so nothing writes to a pane with mu held. A pane that reads slowly
// slows the program down, as a terminal does. One that stops reading is dropped
// after clientWriteTimeout: it can attach again and get the screen as it is then.
type client struct {
	conn   net.Conn
	out    chan []byte
	finish chan struct{} // the server is ending: send what's queued, then close
	gone   chan struct{} // dropped: stop at once
}

func newClient(conn net.Conn) *client {
	return &client{conn: conn, out: make(chan []byte, clientQueue),
		finish: make(chan struct{}), gone: make(chan struct{})}
}

// writes sends a client its queue until it is dropped or the server ends. The
// first item is the replay, which may be large.
func (st *serverState) writes(cl *client) {
	timeout := replayWriteTimeout
	send := func(b []byte) bool {
		_ = cl.conn.SetWriteDeadline(time.Now().Add(timeout))
		timeout = clientWriteTimeout
		_, err := cl.conn.Write(b)
		return err == nil
	}
	defer func() {
		st.mu.Lock()
		st.drop(cl.conn)
		st.mu.Unlock()
	}()
	for {
		select {
		case <-cl.gone:
			return
		case b := <-cl.out:
			if !send(b) {
				return // too slow or gone
			}
		case <-cl.finish:
			for {
				select {
				case b := <-cl.out:
					if !send(b) {
						return
					}
				default:
					return
				}
			}
		}
	}
}

// drop detaches a client. Call with mu held. It does nothing for one already
// dropped.
func (st *serverState) drop(c net.Conn) {
	cl, ok := st.clients[c]
	if !ok {
		return
	}
	delete(st.clients, c)
	close(cl.gone)
	_ = c.Close()
}

// output writes the program's output to the screen and queues it for each pane.
// It is only called from the PTY's read loop, one chunk at a time, so each pane
// gets the chunks in order. Waiting for a full queue happens with mu released.
func (st *serverState) output(p []byte) {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	st.screen.Write(p)
	for _, rep := range st.osc.scan(p) {
		rep.Seq, rep.At = st.agent.Seq+1, time.Now()
		st.agent = rep
	}
	if len(st.clients) == 0 {
		st.mu.Unlock()
		return
	}
	b := append([]byte(nil), p...) // the read loop reuses p
	var full []*client
	for _, cl := range st.clients {
		select {
		case cl.out <- b:
		default:
			full = append(full, cl)
		}
	}
	st.mu.Unlock()

	for _, cl := range full {
		timer := time.NewTimer(clientWriteTimeout)
		select {
		case cl.out <- b:
		case <-cl.gone:
		case <-timer.C:
			st.mu.Lock()
			st.drop(cl.conn) // stopped reading
			st.mu.Unlock()
		}
		timer.Stop()
	}
}

// attach queues the current state for the client and adds it, under one lock,
// so it neither misses nor repeats output. The replay is sent by the client's
// own writer, not with mu held.
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
	cl := newClient(a.conn)
	cl.out <- st.screen.Replay()
	st.clients[a.conn] = cl
	go st.writes(cl)
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

// front names the program in front of the PTY. It reads the PTY's descriptor,
// so like resize it runs with mu held and not once the server is closed: Serve
// closes the PTY after that.
func (st *serverState) front(shellPid int) (Front, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Front{}, false
	}
	return front(st.ptmx, shellPid)
}

func (st *serverState) text() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ""
	}
	return st.screen.Text()
}

// close ends the server's state. Each pane is sent what is already queued for
// it, the program's last output, and is then closed by its writer.
func (st *serverState) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	st.closed = true
	for _, cl := range st.clients {
		close(cl.finish)
	}
	st.screen.Close()
}

// frames handles one client's input, resizes and kill.
func (st *serverState) frames(c net.Conn, hangup func()) {
	defer func() {
		st.mu.Lock()
		st.drop(c)
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

// modeReply answers a mode frame.
type modeReply struct {
	OK bool `json:"ok"`
}

// CheckMode asks the terminal on socket whether it attaches in mode: a
// *ModeError when it doesn't, or when it's older than the question (it
// closes without an answer). ErrEnded when there's no terminal.
func CheckMode(socket, mode string) error {
	if mode == "" || mode == ModeStream {
		return nil // every server has it
	}
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return ErrEnded
	}
	defer c.Close()
	if err := writeFrame(c, frameMode, []byte(mode)); err != nil {
		return ErrEnded
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b, _ := io.ReadAll(c)
	var r modeReply
	if json.Unmarshal(b, &r) != nil || !r.OK {
		return &ModeError{Mode: mode}
	}
	return nil
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
