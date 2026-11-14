// Package daemon is acmd's long-lived state owner: it listens on
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
	"github.com/brettmostert/agent-command/internal/wake"
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
	// Hosts runs hosted sessions; when set, new and resumed
	// sessions are hosted interactive sessions instead of `claude --bg`.
	Hosts Hosts
	// Remover deletes background sessions on purge (claudecli.CLI).
	Remover Remover
	// ClaudeDir is Claude's folder, read for conversations to resume.
	ClaudeDir string
	// Notify configures notifications (nil = none).
	Notify *config.Notify
	// ConfigFile is config.toml: when it changes, notifications follow its [notify]
	// section without a restart. "" keeps Notify as it was given.
	ConfigFile string
	// Fallback shows a notification when no view's sidebar can (terminal-notifier,
	// else osascript).
	Fallback func(ipc.Notice)
	// SendToHost types into a session's terminal (default: through its host).
	SendToHost func(sessionID string, data []byte) error
	// LockWait is how long to wait for a daemon that's on its way out. A hook that
	// replaces an older daemon starts this one straight after asking it to stop.
	LockWait time.Duration
	// ReconcileEvery and TailEvery default to 3 s and 1 s.
	ReconcileEvery time.Duration
	TailEvery      time.Duration
	// FrontEvery is how often terminals are asked what runs in front (2 s), and
	// HostFront asks one (default: through its host).
	FrontEvery time.Duration
	HostFront  func(sessionID string) (term.Front, bool)
	// ScheduleEvery is how often scheduled jobs are checked (15 s), and
	// Location the time zone their schedules are in (the Mac's).
	ScheduleEvery time.Duration
	Location      *time.Location
	// AgentHelp is Claude's --help, to check a job's settings against the
	// installed version ("" or nil: don't check).
	AgentHelp func() string
	// Waker sets the Mac's wakes for jobs (wake.Mac); nil: jobs don't wake it.
	Waker wake.Waker
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
	appWatches   atomic.Int32 // the app's open watches (notify.go)
	appLastWatch atomic.Int64 // when its last one ended, in Unix nanoseconds

	notify     *notifier   // d.mu
	notifyRead time.Time   // config.toml's modification time when notify was read (d.mu)
	bg         *background // d.mu (background.go)

	agentSince map[string]time.Time // front.go: since when an agent without hooks is in front

	wake waking // wake.go
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
	if cfg.FrontEvery == 0 {
		cfg.FrontEvery = 2 * time.Second
	}
	if cfg.ScheduleEvery == 0 {
		cfg.ScheduleEvery = 15 * time.Second
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.HostFront == nil {
		cfg.HostFront = func(id string) (term.Front, bool) { return term.Foreground(cfg.Paths.HostSocket(id)) }
	}
	if err := cfg.Paths.Ensure(); err != nil {
		return err
	}
	unlock, err := lockWaiting(cfg.Paths.Lock(), cfg.LockWait)
	if err != nil {
		return err
	}
	defer unlock()

	st, err := store.Open(ctx, cfg.Paths.DB())
	if err != nil {
		return err
	}
	defer st.Close()

	// Before anything can connect: the app restores its panes from the first list
	// of sessions, and would attach to hosts that an uninstall or a reboot ended.
	check := &reconcile.Reconciler{Store: st, Now: cfg.Now,
		HostAlive: func(s *core.Session) bool { return term.Running(cfg.Paths.HostSocket(s.ID)) }}
	if n, err := check.PauseDeadHosts(ctx); err != nil {
		cfg.Log.Printf("pausing sessions whose host has gone: %v", err)
	} else if n > 0 {
		cfg.Log.Printf("paused %d sessions whose host has gone", n)
	}

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
	d := &Daemon{cfg: cfg, store: st, ln: ln, stop: make(chan struct{}), bg: newBackground()}
	sweepHosts(cfg.Paths)
	_ = os.Remove(cfg.Paths.OldPopupLock())
	if cfg.Notify != nil {
		d.notify = newNotifier(*cfg.Notify)
	}
	d.mu.Lock()
	d.refreshNotify()
	d.mu.Unlock()
	defer os.Remove(sock)

	if err := d.drainSpool(ctx); err != nil {
		cfg.Log.Printf("drain spool: %v", err)
	}
	cfg.Log.Printf("listening on %s (version %s, pid %d)", sock, cfg.Version, os.Getpid())
	go d.background(ctx)
	go d.scheduler(ctx)

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
	case ipc.OpEvent, ipc.OpMutate, ipc.OpNew, ipc.OpResume, ipc.OpInboxAnswer,
		ipc.OpJobSave, ipc.OpJobDelete, ipc.OpJobPause, ipc.OpJobRun:
		return true
	}
	return false
}

func (d *Daemon) handle(ctx context.Context, req ipc.Request) (any, error) {
	if isJobOp(req.Op) {
		return d.handleJob(ctx, req)
	}
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
	case ipc.OpInbox:
		r, err := decode[ipc.InboxRequest](req.Data)
		if err != nil {
			return nil, err
		}
		return d.inboxHistory(ctx, r.SessionID)
	case ipc.OpInboxAnswer:
		a, err := decode[ipc.InboxAnswer](req.Data)
		if err != nil {
			return nil, err
		}
		return nil, d.answerInbox(ctx, a)
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

	sess, isNew := d.hostedSessionOf(ctx, ev.Context), false
	switch {
	case sess != nil && sess.IsTerminal():
		if !sess.TakeAgentEvent(e, ev.Context.ClaudePid) {
			return nil
		}
	case sess != nil:
		if !sess.FollowConversation(e) {
			return nil
		}
	default:
		sess, err = d.store.SessionByClaudeID(ctx, e.SessionID)
		isNew = errors.Is(err, store.ErrNotFound)
		if err != nil && !isNew {
			return err
		}
		if isNew {
			sess = sessionFromHook(e, ev.Context, d.cfg.Now())
		}
	}
	tr := core.Apply(sess, e)
	d.follow(sess, e)
	if tr.Changed() && tr.To.NeedsAttention() {
		sess.Unseen = true
	}
	if isNew {
		err = d.store.CreateSession(ctx, sess)
	} else if tr.Applied {
		err = d.store.UpdateSession(ctx, sess)
	}
	if err != nil {
		return err
	}
	d.transition(sess, tr)
	if tr.Applied { // not a stale event
		if err := d.applyInbox(ctx, sess, e); err != nil {
			return err
		}
	}
	return d.store.AppendEvent(ctx, store.LoggedEvent{
		At: e.Time, SessionID: sess.ID, ClaudeSessionID: e.SessionID, Kind: string(e.Kind),
		Detail: string(sess.Status), ToolName: e.ToolName,
	})
}

// hostedSessionOf is the session a hook's agent runs in, if it names one. This holds
// through /clear, when Claude's own session id changes.
func (d *Daemon) hostedSessionOf(ctx context.Context, hc ipc.HookContext) *core.Session {
	if hc.HostSession == "" {
		return nil
	}
	s, err := d.store.Session(ctx, hc.HostSession)
	if err != nil {
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
	frontT := time.NewTicker(d.cfg.FrontEvery)
	defer reconcileT.Stop()
	defer tailT.Stop()
	defer frontT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case <-reconcileT.C:
			if changed, err := d.tidyInbox(ctx); err != nil {
				d.cfg.Log.Printf("inbox: %v", err)
			} else if changed {
				d.changes.bump()
			}
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
		case <-frontT.C:
			changed, err := d.frontOnce(ctx)
			if err != nil {
				d.cfg.Log.Printf("foreground: %v", err)
			}
			if changed {
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
		}
	}
}

// tailOnce checks the transcripts of busy and waiting sessions for an interrupt
// (no Stop hook fires for it, M0 A12) and marks them done: Esc during a turn, or
// denying a permission or leaving a question.
func (d *Daemon) tailOnce(ctx context.Context, tail *reconcile.Tailer) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, false)
	if err != nil {
		return false, err
	}
	changed := false
	for _, s := range sessions {
		if s.Status != core.StatusBusy && s.Status != core.StatusWaiting {
			tail.Forget(s.TranscriptPath)
			continue
		}
		hit, err := tail.Interrupted(s.TranscriptPath)
		if err != nil || !hit {
			continue
		}
		e := core.Event{Kind: core.EventInterrupted, Time: d.eventTime(s)}
		tr := core.Apply(s, e)
		if !tr.Changed() {
			continue
		}
		d.transition(s, tr)
		// A permission denied or a question left in the session isn't waiting any more.
		if err := d.applyInbox(ctx, s, e); err != nil {
			return changed, err
		}
		s.Unseen = true
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
// from `claude agents --json`.
func sessionFromHook(e core.Event, hc ipc.HookContext, now time.Time) *core.Session {
	s := &core.Session{
		ClaudeSessionID: e.SessionID,
		Cwd:             e.Cwd,
		TranscriptPath:  e.TranscriptPath,
		Kind:            core.KindBackground,
		Pid:             hc.ClaudePid,
		CreatedAt:       now.UTC(),
	}
	return s
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

// lockWaiting takes the lock, waiting a moment if another daemon holds it.
func lockWaiting(path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		unlock, err := lock(path)
		if !errors.Is(err, ErrAlreadyRunning) || !time.Now().Before(deadline) {
			return unlock, err
		}
		time.Sleep(50 * time.Millisecond)
	}
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
