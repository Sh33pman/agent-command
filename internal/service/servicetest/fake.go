// Package servicetest is a fake systemd and launchd for tests: it keeps the
// service's state in memory and records every command.
package servicetest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/brettmostert/agent-command/internal/service"
)

// Fake is a service manager.
type Fake struct {
	mu       sync.Mutex
	GOOS     string
	Home     string
	Enabled  bool // systemd: enabled
	Loaded   bool // launchd: loaded
	Pid      int  // the service's daemon, 0 when stopped
	Linger   bool
	nextPid  int
	calls    []string
	detached []string
	// started and stopped are what the command being answered did.
	started, stopped bool

	// NoSystemd makes Linux run without systemd; WSL makes it WSL.
	NoSystemd, WSL bool
	// LingerFails makes enable-linger fail, as without polkit's consent.
	LingerFails bool
	// BootstrapFails makes launchctl bootstrap fail, as over SSH with no one
	// logged in at the Mac.
	BootstrapFails bool
	// OnStart runs when the fake starts the service's daemon; OnStop when it
	// stops it.
	OnStart func()
	OnStop  func()
	// UsePid, when set, is the pid every start gives the daemon: a test's
	// daemon runs in the test's own process.
	UsePid int
}

// New is a fake for goos, with its files under home.
func New(goos, home string) *Fake {
	return &Fake{GOOS: goos, Home: home, nextPid: 1000}
}

// Env is the service.Env that uses the fake.
func (f *Fake) Env() service.Env {
	return service.Env{
		GOOS: f.GOOS, Home: f.Home, UID: 501, User: "tester",
		Run:       f.run,
		Detach:    f.detach,
		Systemd:   func() bool { return !f.NoSystemd },
		WSL:       f.WSL,
		ReadFile:  os.ReadFile,
		WriteFile: os.WriteFile,
		Remove:    os.Remove,
	}
}

// Calls are the commands run, as "name arg…".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Detached are the scripts started to outlive the caller.
func (f *Fake) Detached() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.detached...)
}

// Running says whether the service's daemon runs.
func (f *Fake) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Pid != 0
}

func (f *Fake) detach(script string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detached = append(f.detached, script)
	return nil
}

var errExit = errors.New("exit status 1")

func (f *Fake) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	f.started, f.stopped = false, false
	out, err := f.answer(name, args)
	started, stopped := f.started, f.stopped
	onStart, onStop := f.OnStart, f.OnStop
	f.mu.Unlock()
	if stopped && onStop != nil {
		onStop()
	}
	if started && onStart != nil {
		onStart()
	}
	return out, err
}

// answer is what a command says. It sets started and stopped when it starts
// or stops the daemon. f.mu is held.
func (f *Fake) answer(name string, args []string) ([]byte, error) {
	start := func() {
		if f.Pid != 0 {
			return
		}
		f.nextPid++
		f.Pid = f.nextPid
		if f.UsePid != 0 {
			f.Pid = f.UsePid
		}
		f.started = true
	}
	stop := func() {
		if f.Pid != 0 {
			f.Pid = 0
			f.stopped = true
		}
	}
	cmd := strings.Join(args, " ")
	switch name {
	case "systemctl":
		cmd = strings.TrimPrefix(cmd, "--user ")
		switch {
		case cmd == "daemon-reload":
		case cmd == "enable "+service.Unit:
			f.Enabled = true
		case cmd == "disable --now "+service.Unit:
			f.Enabled = false
			stop()
		case cmd == "start "+service.Unit:
			start()
		case cmd == "stop "+service.Unit:
			stop()
		case cmd == "restart "+service.Unit:
			stop()
			start()
		case strings.HasPrefix(cmd, "show --property=MainPID"):
			return fmt.Appendf(nil, "%d\n", f.Pid), nil
		default:
			return []byte("unknown command " + cmd), errExit
		}
		return nil, nil
	case "loginctl":
		switch {
		case strings.HasPrefix(cmd, "show-user"):
			if f.Linger {
				return []byte("yes\n"), nil
			}
			return []byte("no\n"), nil
		case cmd == "enable-linger":
			if f.LingerFails {
				return []byte("Could not enable linger: Access denied"), errExit
			}
			f.Linger = true
			return nil, nil
		}
	case "launchctl":
		target := "gui/501/" + service.Label
		switch {
		case cmd == "print "+target:
			if !f.Loaded {
				return []byte("Could not find service"), errExit
			}
			if f.Pid == 0 {
				return []byte("state = not running\n"), nil
			}
			return fmt.Appendf(nil, "\tstate = running\n\tpid = %d\n", f.Pid), nil
		case strings.HasPrefix(cmd, "bootstrap gui/501 "):
			if f.BootstrapFails {
				return []byte("Bootstrap failed: 125: Domain does not support specified action"), errExit
			}
			if f.Loaded {
				return []byte("Bootstrap failed: 5: Input/output error"), errExit
			}
			f.Loaded = true
			start() // RunAtLoad
			return nil, nil
		case cmd == "bootout "+target:
			if !f.Loaded {
				return []byte("Boot-out failed: 3: No such process"), errExit
			}
			f.Loaded = false
			stop()
			return nil, nil
		case cmd == "kickstart "+target:
			start()
			return nil, nil
		case cmd == "kickstart -k "+target:
			stop()
			start()
			return nil, nil
		}
	}
	return []byte("the fake doesn't know " + name + " " + cmd), errExit
}
