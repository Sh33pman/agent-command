package term

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
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
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return err
	}
	defer ptmx.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	hangup := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP) // its process group (pty.Start sets setsid)
		}
	}
	if s.Ready != nil {
		s.Ready()
	}

	var mu sync.Mutex
	var client net.Conn
	kept := &ring{n: s.Keep}
	output := make(chan struct{})
	go func() { // the shell's output: keep it, pass it on
		defer close(output)
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				kept.Write(buf[:n])
				if client != nil {
					_ = client.SetWriteDeadline(time.Now().Add(2 * time.Second))
					if _, err := client.Write(buf[:n]); err != nil {
						_ = client.Close() // too slow or gone: it can attach again
						client = nil
					}
				}
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	conns := make(chan net.Conn)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { // only an attach becomes the client
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				typ, _, err := readFrame(c)
				_ = c.SetReadDeadline(time.Time{})
				switch {
				case err == nil && typ == frameAttach:
					conns <- c
				case err == nil && typ == frameKill:
					hangup()
					_ = c.Close()
				default:
					_ = c.Close() // a probe
				}
			}()
		}
	}()

	var size pty.Winsize
	for {
		select {
		case <-ctx.Done():
			hangup()
			<-exited
			return s.end(&mu, &client)
		case <-exited:
			<-output
			return s.end(&mu, &client)
		case c := <-conns:
			mu.Lock()
			if client != nil {
				_ = client.Close() // one client at a time: the newest wins
			}
			client = c
			_, _ = c.Write(append([]byte("\x1b[H\x1b[2J"), kept.Bytes()...))
			mu.Unlock()
			go s.frames(c, ptmx, &mu, &size, hangup)
		}
	}
}

func (s *Server) end(mu *sync.Mutex, client *net.Conn) error {
	mu.Lock()
	if *client != nil {
		_ = (*client).Close()
	}
	mu.Unlock()
	if s.Ended != nil {
		s.Ended()
	}
	return nil
}

// frames handles one client's input, resizes and kill.
func (s *Server) frames(c net.Conn, ptmx *os.File, mu *sync.Mutex, size *pty.Winsize, hangup func()) {
	for {
		typ, p, err := readFrame(c)
		if err != nil {
			return
		}
		switch typ {
		case frameInput:
			_, _ = ptmx.Write(p)
		case frameResize:
			if len(p) != 4 {
				continue
			}
			ws := pty.Winsize{Rows: binary.BigEndian.Uint16(p), Cols: binary.BigEndian.Uint16(p[2:])}
			mu.Lock()
			if ws == *size && ws.Rows > 1 {
				// Same size (another session's pane): nudge so full-screen
				// programs redraw.
				nudge := ws
				nudge.Rows--
				_ = pty.Setsize(ptmx, &nudge)
			}
			_ = pty.Setsize(ptmx, &ws)
			*size = ws
			mu.Unlock()
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
