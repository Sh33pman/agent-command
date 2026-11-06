// Package daemon is acmd's long-lived state owner (SPEC §4.1): it listens on
// a user-only unix socket, applies hook events to the state machine and
// persists the result. One instance per user, enforced with a lock file.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/reconcile"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// ErrAlreadyRunning means another daemon holds the lock.
var ErrAlreadyRunning = errors.New("daemon already running")

// Config configures a daemon.
type Config struct {
	Paths   paths.Paths
	Version string
	Log     *log.Logger
	Now     func() time.Time
	// Agents lists Claude sessions for the reconciler (claudecli.CLI); nil
	// disables reconciliation (e.g. claude not found).
	Agents reconcile.Lister
	// Stopper stops background sessions on archive (claudecli.CLI).
	Stopper Stopper
	// Hosts runs hosted sessions (SPEC §4.5); when set, new and resumed
	// sessions are hosted interactive sessions instead of `claude --bg`.
	Hosts Hosts
	// Remover deletes background sessions on purge (claudecli.CLI).
	Remover Remover
	// ClaudeDir is Claude's folder, read for conversations to resume.
	ClaudeDir string
	// Notify configures notifications (nil = none).
	Notify *config.Notify
	// Fallback shows a notification when no view's sidebar can (SPEC §8.5:
	// terminal-notifier, else osascript).
	Fallback func(ipc.Notice)
	// ReconcileEvery and TailEvery default to 3 s and 1 s (SPEC §6.7, §5.4).
	ReconcileEvery time.Duration
	TailEvery      time.Duration
}

// Daemon is a running daemon.
type Daemon struct {
	cfg   Config
	store *store.Store
	ln    net.Listener
	mu    sync.Mutex // serializes state changes
	stop  chan struct{}
	once  sync.Once

	changes      changes      // bumped on every state change (OpWatch)
	seen         seen         // view heartbeats
	notes        notes        // status bar messages per view (not persisted)
	terms        terms        // sessions with a running terminal (not persisted)
	appWatches   atomic.Int32 // the app's open watches (notify.go)
	appLastWatch atomic.Int64 // when its last one ended, in Unix nanoseconds
	inbox        inbox        // what the reply popup shows (not persisted)

	notify *notifier // d.mu
}

// Run starts the daemon and blocks until ctx is canceled or a client asks
// it to shut down.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Log == nil {
		cfg.Log = log.New(os.Stderr, "acmd daemon: ", log.LstdFlags)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ReconcileEvery == 0 {
		cfg.ReconcileEvery = 3 * time.Second
	}
	if cfg.TailEvery == 0 {
		cfg.TailEvery = time.Second
	}
	if err := cfg.Paths.Ensure(); err != nil {
		return err
	}
	unlock, err := lock(cfg.Paths.Lock())
	if err != nil {
		return err
	}
	defer unlock()

	st, err := store.Open(ctx, cfg.Paths.DB())
	if err != nil {
		return err
	}
	defer st.Close()

	sock := cfg.Paths.Socket()
	_ = os.Remove(sock) // stale socket from a crashed daemon; we hold the lock
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	d := &Daemon{cfg: cfg, store: st, ln: ln, stop: make(chan struct{}),
		seen: seen{at: map[string]time.Time{}, start: cfg.Now()}}
	d.terms.rescan(cfg.Paths)
	sweepHosts(cfg.Paths)
	if cfg.Notify != nil {
		d.notify = newNotifier(*cfg.Notify)
	}
	defer os.Remove(sock)

	if err := d.drainSpool(ctx); err != nil {
		cfg.Log.Printf("drain spool: %v", err)
	}
	cfg.Log.Printf("listening on %s (version %s, pid %d)", sock, cfg.Version, os.Getpid())
	go d.background(ctx)

	go func() {
		select {
		case <-ctx.Done():
		case <-d.stop:
		}
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	var connsMu sync.Mutex
	conns := map[net.Conn]bool{}
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
			case <-d.stop:
			default:
				cfg.Log.Printf("accept: %v", err)
			}
			break
		}
		connsMu.Lock()
		conns[c] = true
		connsMu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.serve(ctx, c)
			connsMu.Lock()
			delete(conns, c)
			connsMu.Unlock()
		}()
	}
	// Let requests in flight finish, then close what's left: clients that
	// keep a connection open between requests (sidebars, the popup) would
	// otherwise keep the daemon from ever stopping.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		connsMu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		connsMu.Unlock()
		<-done
	}
	cfg.Log.Printf("stopped")
	return nil
}

// shutdownGrace is how long a stopping daemon lets requests finish before
// it closes the connections left open.
var shutdownGrace = 2 * time.Second

func (d *Daemon) serve(ctx context.Context, c net.Conn) {
	defer c.Close()
	if err := checkPeer(c); err != nil {
		d.cfg.Log.Printf("rejected connection: %v", err)
		return
	}
	r := bufio.NewReaderSize(c, 64*1024)
	for {
		var req ipc.Request
		if err := ipc.ReadJSON(r, &req); err != nil {
			return
		}
		data, err := d.handle(ctx, req)
		if err == nil && changesState(req.Op) {
			d.changes.bump()
		}
		resp := ipc.Response{OK: err == nil}
		if err != nil {
			resp.Error = err.Error()
		} else if data != nil {
			b, merr := json.Marshal(data)
			if merr != nil {
				resp = ipc.Response{Error: merr.Error()}
			} else {
				resp.Data = b
			}
		}
		if err := ipc.WriteJSON(c, resp); err != nil {
			return
		}
		if req.Op == ipc.OpShutdown {
			d.once.Do(func() { close(d.stop) })
			return
		}
	}
}

// changesState reports whether a successful request may have changed state.
func changesState(op string) bool {
	switch op {
	case ipc.OpEvent, ipc.OpMutate, ipc.OpNew, ipc.OpResume:
		return true
	}
	return false
}

func (d *Daemon) handle(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpHello:
		return ipc.Hello{Proto: ipc.Proto, Version: d.cfg.Version, Pid: os.Getpid()}, nil
	case ipc.OpPing, ipc.OpShutdown:
		return nil, nil
	case ipc.OpEvent:
		var ev ipc.HookEvent
		if err := json.Unmarshal(req.Data, &ev); err != nil {
			return nil, fmt.Errorf("bad event: %w", err)
		}
		return nil, d.ApplyHook(ctx, ev)
	case ipc.OpSnapshot:
		return d.snapshot(ctx)
	case ipc.OpMutate:
		var m ipc.Mutation
		if err := json.Unmarshal(req.Data, &m); err != nil {
			return nil, fmt.Errorf("bad mutation: %w", err)
		}
		return d.mutate(ctx, m)
	case ipc.OpWatch:
		w, err := decode[ipc.WatchRequest](req.Data)
		if err != nil {
			return nil, err
		}
		return d.watch(ctx, w)
	case ipc.OpNew:
		n, err := decode[ipc.NewRequest](req.Data)
		if err != nil {
			return nil, err
		}
		return d.newSession(ctx, n)
	case ipc.OpConversations:
		return d.conversations(ctx)
	case ipc.OpLayout:
		d.mu.Lock()
		defer d.mu.Unlock()
		value, err := d.store.AppState(ctx, layoutKey)
		return ipc.Layout{JSON: value}, err
	case ipc.OpSetLayout:
		l, err := decode[ipc.Layout](req.Data)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.store.SetAppState(ctx, layoutKey, l.JSON)
	case ipc.OpResume:
		m, err := decode[ipc.Mutation](req.Data)
		if err != nil {
			return nil, err
		}
		return d.resume(ctx, m.ID)
	case ipc.OpStatus:
		sessions, err := d.store.Sessions(ctx, true)
		if err != nil {
			return nil, err
		}
		events, err := d.store.EventCount(ctx)
		if err != nil {
			return nil, err
		}
		return ipc.Status{Version: d.cfg.Version, Pid: os.Getpid(), Sessions: len(sessions), Events: events}, nil
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
}

// layoutKey is where the app's pane layout is kept.
const layoutKey = "layout"

// ApplyHook parses a hook event, finds or creates its session, applies the
// state machine and persists the result.
func (d *Daemon) ApplyHook(ctx context.Context, ev ipc.HookEvent) error {
	e, err := core.ParseHook(ev.Name, ev.Payload, ev.At)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	sess, isNew, moved := d.terminalOf(ctx, ev.Context), false, false
	if sess != nil {
		if !sess.TakeAgentEvent(e, ev.Context.ClaudePid) {
			return nil
		}
	} else {
		sess, err = d.store.SessionByClaudeID(ctx, e.SessionID)
		isNew = errors.Is(err, store.ErrNotFound)
		if err != nil && !isNew {
			return err
		}
		if isNew {
			sess = sessionFromHook(e, ev.Context, d.cfg.Now())
		} else {
			moved = refreshPane(sess, ev.Context)
		}
	}
	tr := core.Apply(sess, e)
	shown := d.shown(ctx, sess.ID)
	if tr.Changed() && tr.To.NeedsAttention() && !shown {
		sess.Unseen = true
	}
	if isNew {
		err = d.store.CreateSession(ctx, sess)
	} else if tr.Applied || moved {
		err = d.store.UpdateSession(ctx, sess)
	}
	if err != nil {
		return err
	}
	d.transition(ctx, sess, tr)
	if tr.Applied { // not a stale event
		d.inbox.apply(sess, e)
	}
	return d.store.AppendEvent(ctx, store.LoggedEvent{
		At: e.Time, SessionID: sess.ID, ClaudeSessionID: e.SessionID, Kind: string(e.Kind),
		Detail: string(sess.Status), ToolName: e.ToolName,
	})
}

// terminalOf is the terminal session a hook's agent runs in, if it's in one.
func (d *Daemon) terminalOf(ctx context.Context, hc ipc.HookContext) *core.Session {
	if hc.HostSession == "" {
		return nil
	}
	s, err := d.store.Session(ctx, hc.HostSession)
	if err != nil || !s.IsTerminal() {
		return nil
	}
	return s
}

// background runs the reconciler and the transcript tail until the daemon
// stops. Both take d.mu, like hook handling, so updates never interleave.
func (d *Daemon) background(ctx context.Context) {
	var rec *reconcile.Reconciler
	if d.cfg.Agents != nil {
		rec = &reconcile.Reconciler{Store: d.store, Agents: d.cfg.Agents, Now: d.cfg.Now,
			HostAlive: func(s *core.Session) bool { return term.Running(d.cfg.Paths.HostSocket(s.ID)) }}
	}
	tail := reconcile.NewTailer()
	reconcileT := time.NewTicker(d.cfg.ReconcileEvery)
	tailT := time.NewTicker(d.cfg.TailEvery)
	pruneT := time.NewTicker(10 * time.Second)
	defer reconcileT.Stop()
	defer tailT.Stop()
	defer pruneT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case <-reconcileT.C:
			if rec == nil {
				continue
			}
			d.mu.Lock()
			res, err := rec.Once(ctx)
			d.mu.Unlock()
			if err != nil {
				d.cfg.Log.Printf("reconcile: %v", err)
			} else if res.Discovered+res.Updated > 0 {
				d.cfg.Log.Printf("reconcile: %d discovered, %d updated", res.Discovered, res.Updated)
				d.changes.bump()
			}
		case <-tailT.C:
			changed, err := d.tailOnce(ctx, tail)
			if err != nil {
				d.cfg.Log.Printf("transcript tail: %v", err)
			}
			if changed {
				d.changes.bump()
			}
		case <-pruneT.C:
			n, err := d.pruneViews(ctx)
			if err != nil {
				d.cfg.Log.Printf("prune views: %v", err)
			}
			if n > 0 {
				d.cfg.Log.Printf("pruned %d closed views", n)
				d.changes.bump()
			}
		}
	}
}

// tailOnce checks busy sessions' transcripts for an Esc interrupt (no Stop
// hook fires for it, M0 A12) and marks them done.
func (d *Daemon) tailOnce(ctx context.Context, tail *reconcile.Tailer) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, false)
	if err != nil {
		return false, err
	}
	changed := false
	for _, s := range sessions {
		if s.Status != core.StatusBusy {
			tail.Forget(s.TranscriptPath)
			continue
		}
		hit, err := tail.Interrupted(s.TranscriptPath)
		if err != nil || !hit {
			continue
		}
		tr := core.Apply(s, core.Event{Kind: core.EventInterrupted, Time: d.eventTime(s)})
		if !tr.Changed() {
			continue
		}
		d.transition(ctx, s, tr)
		s.Unseen = !d.shown(ctx, s.ID)
		changed = true
		if err := d.store.UpdateSession(ctx, s); err != nil {
			return changed, err
		}
		if err := d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
			Kind: string(core.EventInterrupted), Detail: string(s.Status)}); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// sessionFromHook creates the row for a Claude session acmd hasn't seen yet.
// Classification here is provisional: the reconciler (M1d) corrects Kind
// from `claude agents --json` (SPEC §6.3).
func sessionFromHook(e core.Event, hc ipc.HookContext, now time.Time) *core.Session {
	s := &core.Session{
		ClaudeSessionID: e.SessionID,
		Cwd:             e.Cwd,
		TranscriptPath:  e.TranscriptPath,
		Kind:            core.KindBackground,
		Pid:             hc.ClaudePid,
		CreatedAt:       now.UTC(),
	}
	if loc := paneFromHook(hc); loc != nil {
		s.Kind = core.KindExternal
		s.External = loc
	}
	return s
}

// paneFromHook is the user's own pane a hook ran in (nil when it ran in
// one of acmd's views, or the terminal is unknown).
func paneFromHook(hc ipc.HookContext) *core.PaneLoc {
	if hc.ViewID != "" || (hc.KittyWindowID == "" && hc.TermProgram == "") {
		return nil
	}
	loc := &core.PaneLoc{Terminal: terminalName(hc.TermProgram, hc.KittyWindowID), TTY: hc.TTY}
	if hc.KittyWindowID != "" {
		loc.KittySocket = hc.KittyListenOn
		_, _ = fmt.Sscan(hc.KittyWindowID, &loc.KittyWindow)
	}
	return loc
}

// refreshPane keeps an external session's pane current: a conversation
// continued in another window (or after the terminal restarted) reports
// its new pane in every hook. It reports whether anything changed.
func refreshPane(s *core.Session, hc ipc.HookContext) bool {
	loc := paneFromHook(hc)
	if s.Kind != core.KindExternal || loc == nil {
		return false
	}
	changed := false
	if s.External == nil || *s.External != *loc {
		if s.External != nil && loc.KittySocket == "" && loc.KittyWindow == s.External.KittyWindow {
			loc.KittySocket = s.External.KittySocket // a hook without the socket
		}
		if s.External == nil || *s.External != *loc {
			s.External, changed = loc, true
		}
	}
	if hc.ClaudePid > 0 && s.Pid != hc.ClaudePid {
		s.Pid, changed = hc.ClaudePid, true
	}
	return changed
}

func terminalName(termProgram, kittyWindow string) string {
	switch {
	case kittyWindow != "":
		return "kitty"
	case termProgram == "ghostty":
		return "ghostty"
	default:
		return "unknown"
	}
}

// drainSpool applies events that hooks spooled while the daemon was down.
// The file is renamed first so hooks can keep appending to a fresh spool.
func (d *Daemon) drainSpool(ctx context.Context) error {
	spool := d.cfg.Paths.Spool()
	draining := spool + ".draining"
	if err := os.Rename(spool, draining); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A previous drain may have been interrupted.
			if _, serr := os.Stat(draining); serr != nil {
				return nil
			}
		} else {
			return err
		}
	}
	f, err := os.Open(filepath.Clean(draining))
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), core.MaxHookPayload*2)
	n, bad := 0, 0
	for sc.Scan() {
		var ev ipc.HookEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			bad++
			continue
		}
		if err := d.ApplyHook(ctx, ev); err != nil {
			bad++
			continue
		}
		n++
	}
	scanErr := sc.Err()
	_ = f.Close()
	if scanErr != nil {
		return scanErr
	}
	d.cfg.Log.Printf("drained spool: %d events applied, %d skipped", n, bad)
	return os.Remove(draining)
}

// lock takes an exclusive, non-blocking flock on path.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

// Running reports whether a daemon holds the lock in p (a stopping daemon
// holds it until it has fully exited).
func Running(p paths.Paths) bool {
	unlock, err := lock(p.Lock())
	if err != nil {
		return errors.Is(err, ErrAlreadyRunning)
	}
	unlock()
	return false
}
