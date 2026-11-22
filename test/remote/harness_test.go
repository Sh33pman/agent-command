// Package remote runs Remote sessions end to end: real acmd daemons, a host
// and a client, each with its own folders and identity, on one machine and
// over real TCP on 127.0.0.1. Sessions run sh (the terminal harness), so
// neither claude nor agy is needed. `go test -short` skips it.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/term"
	"github.com/brettmostert/agent-command/internal/version"
)

// bin is the agent-command binary every test runs, built once.
var (
	bin        string
	binVersion string
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		fmt.Println("skipped: builds and runs the binary")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "acmdbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin = filepath.Join(dir, "agent-command")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/agent-command")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.Exit(1)
	}
	out, err := exec.Command(bin, "version", "--json").Output()
	var info version.Info
	if err == nil {
		err = json.Unmarshal(out, &info)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "version: %v\n", err)
		os.Exit(1)
	}
	binVersion = info.Version
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// machine is one acmd: its folders, and its daemon while it runs.
type machine struct {
	t    *testing.T
	name string
	dir  string
	home string
	p    paths.Paths
	env  []string

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan error
}

// newMachine makes a machine's folders under a short temporary folder: the
// unix sockets in its runtime folder must fit in 104 bytes on macOS. config
// is its config.toml.
func newMachine(t *testing.T, name, config string) *machine {
	t.Helper()
	dir, err := os.MkdirTemp("", "ar")
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{t: t, name: name, dir: dir, home: filepath.Join(dir, "home"),
		p: paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}}
	for _, d := range []string{m.home, m.p.ConfigDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(m.p.Config(), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only what acmd needs, so nothing of this machine's own setup leaks in: no
	// claude or agy on PATH, a plain sh for terminals.
	m.env = []string{
		"HOME=" + m.home, "PATH=/usr/bin:/bin", "SHELL=/bin/sh", "PS1=$ ", "LANG=C", "TMPDIR=" + os.TempDir(),
		"ACMD_STATE_DIR=" + m.p.StateDir, "ACMD_RUNTIME_DIR=" + m.p.RuntimeDir, "ACMD_CONFIG_DIR=" + m.p.ConfigDir,
	}
	t.Cleanup(func() {
		if t.Failed() {
			log, _ := os.ReadFile(m.p.DaemonLog())
			t.Logf("%s's daemon.log:\n%s", name, log)
		}
		m.endSessions()
		m.stop()
		m.checkClean()
		_ = os.RemoveAll(dir)
	})
	return m
}

// hostConfig is a config.toml with Hosting on at port.
func hostConfig(port int, fallback bool) string {
	return fmt.Sprintf(`[notify]
enabled = false

[remote.host]
enabled = true
listen = "localhost"
port = %d
port_fallback = %t
`, port, fallback)
}

// clientConfig is a config.toml for a machine that only uses hosts.
const clientConfig = "[notify]\nenabled = false\n"

// start runs the daemon and waits until it answers.
func (m *machine) start() {
	m.t.Helper()
	cmd := exec.Command(bin, "daemon")
	cmd.Env = m.env
	cmd.Dir = m.home
	if err := cmd.Start(); err != nil {
		m.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	m.mu.Lock()
	m.cmd, m.done = cmd, done
	m.mu.Unlock()
	m.waitFor("its daemon", func() bool {
		select {
		case err := <-done:
			done <- err
			m.t.Fatalf("%s's daemon exited: %v", m.name, err)
		default:
		}
		c, err := m.dial()
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
}

// stop shuts the daemon down and waits for it to exit. Session hosts keep
// running, as they do when a user restarts the daemon.
func (m *machine) stop() {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	m.cmd, m.done = nil, nil
	m.mu.Unlock()
	if cmd == nil {
		return
	}
	if c, err := m.dial(); err == nil {
		_ = c.Call(ipc.OpShutdown, nil, nil)
		_ = c.Close()
	}
	select {
	case err := <-done:
		if err != nil {
			m.t.Errorf("%s's daemon exited with %v", m.name, err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		m.t.Logf("killed: %v", <-done)
		m.t.Errorf("%s's daemon didn't shut down", m.name)
	}
}

// endSessions ends every session host this machine runs, and waits until
// their sockets are gone. A host writes an .id file next to its socket; the
// client daemon's proxy sockets have none, and go with the daemon.
func (m *machine) endSessions() {
	ids, _ := filepath.Glob(filepath.Join(m.p.RuntimeDir, "h-*.id"))
	var socks []string
	for _, id := range ids {
		sock := strings.TrimSuffix(id, ".id") + ".sock"
		_ = term.Kill(sock)
		socks = append(socks, sock)
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, sock := range socks {
		for term.Running(sock) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if term.Running(sock) {
			m.t.Errorf("%s: the session host on %s didn't end", m.name, sock)
		}
	}
}

// checkClean fails when a socket is left behind once everything stopped.
func (m *machine) checkClean() {
	left, _ := filepath.Glob(filepath.Join(m.p.RuntimeDir, "*.sock"))
	for _, s := range left {
		if term.Running(s) {
			m.t.Errorf("%s: %s still answers", m.name, s)
		}
	}
}

func (m *machine) dial() (*ipc.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ipc.Dial(ctx, m.p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: binVersion})
}

// try is one ipc call on a connection of its own.
func (m *machine) try(op string, in, out any) error {
	c, err := m.dial()
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Call(op, in, out)
}

// call is try that must succeed.
func (m *machine) call(op string, in, out any) {
	m.t.Helper()
	if err := m.try(op, in, out); err != nil {
		m.t.Fatalf("%s: %s: %v", m.name, op, err)
	}
}

func (m *machine) snapshot() ipc.Snapshot {
	m.t.Helper()
	var snap ipc.Snapshot
	m.call(ipc.OpSnapshot, nil, &snap)
	return snap
}

func (m *machine) status() ipc.RemoteStatus {
	m.t.Helper()
	var st ipc.RemoteStatus
	m.call(ipc.OpRemoteStatus, nil, &st)
	return st
}

// listening waits for Hosting to listen, and returns its status.
func (m *machine) listening() ipc.RemoteStatus {
	m.t.Helper()
	var st ipc.RemoteStatus
	m.waitFor("Hosting to listen", func() bool {
		st = m.status()
		return st.Listening
	})
	return st
}

// address is where clients reach this machine's Hosting.
func (m *machine) address() string {
	return fmt.Sprintf("127.0.0.1:%d", m.listening().Port)
}

// identity is this machine's own identity, made now if it has none yet (as
// `acmd remote identity` does).
func (m *machine) identity() *identity.Identity {
	m.t.Helper()
	id, err := identity.Ensure(m.p.RemoteIdentityDir())
	if err != nil {
		m.t.Fatal(err)
	}
	return id
}

// newSession starts a terminal session in dir and waits for its host.
func (m *machine) newSession(name string, remote bool) *ipc.Session {
	m.t.Helper()
	dir := filepath.Join(m.home, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.t.Fatal(err)
	}
	var s ipc.Session
	m.call(ipc.OpNew, ipc.NewRequest{Cwd: dir, Harness: core.HarnessTerminal, Name: name, Remote: &remote}, &s)
	m.waitFor("the session's host", func() bool { return term.Running(m.p.HostSocket(s.ID)) })
	return &s
}

// screen is a session's screen and history, on the host.
func (m *machine) screen(id string) string {
	text, _ := term.ScreenText(m.p.HostSocket(id))
	return text
}

// typeIn types into a session on the host, without attaching.
func (m *machine) typeIn(id, text string) {
	m.t.Helper()
	if err := term.Send(m.p.HostSocket(id), []byte(text)); err != nil {
		m.t.Fatal(err)
	}
}

// knownHost is this client's view of a known host.
func (m *machine) knownHost(name string) ipc.RemoteHost {
	m.t.Helper()
	var hosts []ipc.RemoteHost
	m.call(ipc.OpRemoteHosts, nil, &hosts)
	for _, h := range hosts {
		if h.Name == name {
			return h
		}
	}
	m.t.Fatalf("%s: no known host %q in %+v", m.name, name, hosts)
	return ipc.RemoteHost{}
}

// addHost adds host as a known host named name, and browses it so the
// client connects.
func (m *machine) addHost(name string, host *machine) ipc.RemoteHost {
	m.t.Helper()
	var rh ipc.RemoteHost
	m.call(ipc.OpRemoteHostAdd, ipc.RemoteHostEdit{Name: name, Address: host.address(), Fingerprint: host.listening().Fingerprint}, &rh)
	_ = m.try(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: name}, nil) // wants the host: it may well not answer yet
	return rh
}

// waitHost waits for a known host to be in state.
func (m *machine) waitHost(name, state string) ipc.RemoteHost {
	m.t.Helper()
	var h ipc.RemoteHost
	m.waitFor(name+" to be "+state, func() bool {
		h = m.knownHost(name)
		return h.State == state
	})
	return h
}

// pair adds host to this client and authorizes the client there by its
// fingerprint, then waits until it's connected.
func (m *machine) pair(name string, host *machine) {
	m.t.Helper()
	host.call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: m.identity().Fingerprint(), Name: m.name}, nil)
	m.addHost(name, host)
	m.waitHost(name, ipc.HostConnected)
}

// session is a session in this machine's snapshot.
func (m *machine) session(id string) *ipc.Session {
	m.t.Helper()
	for _, s := range m.snapshot().Sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// waitFor polls cond until it holds, for up to 15 seconds.
func (m *machine) waitFor(what string, cond func() bool) {
	m.t.Helper()
	waitFor(m.t, m.name+": "+what, cond)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// freePort is a TCP port on 127.0.0.1 that nothing listens on now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// hold listens on a free port until the test ends, so Hosting finds it taken.
func hold(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// pane is a terminal attached to a session's socket, as the app's pane is.
type pane struct {
	out    syncBuf
	in     chan []byte
	size   chan [2]int
	done   chan error
	cancel context.CancelFunc
}

func attach(t *testing.T, socket string, rows, cols int) *pane {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &pane{in: make(chan []byte, 8), size: make(chan [2]int, 1), done: make(chan error, 1), cancel: cancel}
	go func() { p.done <- term.Attach(ctx, socket, p.in, p.size, rows, cols, &p.out) }()
	t.Cleanup(func() {
		cancel()
		if err := <-p.done; err != nil {
			t.Logf("the pane ended with %v", err)
		}
	})
	return p
}

// sees waits for text in the pane's output.
func (p *pane) sees(t *testing.T, text string) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%q in the pane", text), func() bool { return strings.Contains(p.out.String(), text) })
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// errText is what an error says, the daemon's own message for an ipc one.
func errText(err error) string {
	var re *ipc.RemoteError
	if errors.As(err, &re) {
		return re.Msg
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
