// Package daemon is acmd's long-lived state owner: it listens on
// a user-only unix socket, applies hook events to the state machine and
// persists the result. One instance per user, enforced with a lock file.
package daemon

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/peer"
	"github.com/brettmostert/agent-command/internal/proc"
	"github.com/brettmostert/agent-command/internal/reconcile"
	"github.com/brettmostert/agent-command/internal/remote/client"
	"github.com/brettmostert/agent-command/internal/remote/host"
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
	// ClaudeDir is Claude's folder, and AgyDir agy's (agycli.Dir), read for
	// conversations to resume. AgyDir "" leaves agy's out.
	ClaudeDir string
	AgyDir    string
	// Notify configures notifications (nil = none).
	Notify *config.Notify
	// ConfigFile is config.toml. The daemon is its only writer (config.set),
	// and follows it when it's edited by hand. "" keeps Notify as it was
	// given, and refuses changes.
	ConfigFile string
	// ResolveEditor finds an editor's launcher when one is chosen
	// (editor.preset or editor.command), and says if it isn't installed.
	// nil leaves editor.resolved as it is.
	ResolveEditor func(config.Editor) (config.Editor, error)
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
	// HostAgent asks a session's host for its agent's last OSC 7501 report,
	// on the same tick (default: through its host).
	HostAgent func(sessionID string) (term.AgentState, bool)
	// ProcInfo looks up a process, to tell who's on the socket (default:
	// proc.Lookup).
	ProcInfo func(pid int) (proc.Proc, error)
	// HostScreen reads a session's screen as text when its agent reports it
	// waits on you, for the answers it shows (default: through its host).
	HostScreen func(sessionID string) (string, bool)
	// ScheduleEvery is how often scheduled jobs are checked (15 s), and
	// Location the time zone their schedules are in (the Mac's).
	ScheduleEvery time.Duration
	Location      *time.Location
	// AgentHelp is an agent's --help, by harness ("claude" or "antigravity"), to
	// check a job's settings against the installed version ("" or nil: don't check).
	AgentHelp func(harness string) string
	// Waker sets the Mac's wakes for jobs (wake.Mac); nil: jobs don't wake it.
	Waker wake.Waker
	// OnRemoteChanged, when set, is subscribed to Daemon.OnRemoteChanged from the start.
	OnRemoteChanged func(RemoteChange)
	// Harnesses are the harnesses installed here ("claude", "antigravity",
	// "terminal"), which Hosting's clients may start sessions with. nil: all.
	Harnesses func() []string
	// Home is where the host API's folders start (default: $HOME).
	Home string

	// CheckHealth checks acmd's setup (doctor.Health): the agents' hooks,
	// and whether to offer to set up. nil checks nothing.
	CheckHealth func(ctx context.Context) []ipc.Health
	// HealthFiles are the agents' settings files: a change checks again.
	HealthFiles []string
	// Doctor makes acmd doctor's checks (the doctor op). nil refuses.
	Doctor func(ctx context.Context) ipc.DoctorReport
	// OpenEditor opens a folder or a file in the editor chosen, on this
	// machine (the editor.open op): Path is absolute, a session's folder is
	// already resolved. nil refuses.
	OpenEditor func(ctx context.Context, r ipc.EditorOpen) error
	// Init sets acmd up as `acmd init` does (the init op). nil refuses.
	Init func(ctx context.Context) (ipc.InitResult, error)
	// OSVersion is the OS's version, for reports ("" when unknown).
	OSVersion func(ctx context.Context) string
	// Speaker is the speech engine for the settings given (default: the
	// speech helper, internal/speech).
	Speaker func(config.Config) Speaker
	// ReleasesURL is where updates are looked for (the Homebrew tap's
	// releases), and HTTP what asks. "" is the tap.
	ReleasesURL string
	HTTP        *http.Client
}

// Daemon is a running daemon.
type Daemon struct {
	cfg      Config
	store    *store.Store
	requests requests // the results of retryable ops that carried a request id
	ln       net.Listener
	mu       sync.Mutex // serializes state changes
	stop     chan struct{}
	once     sync.Once

	changes changes      // bumped on every state change (OpWatch)
	reg     *registry    // the clients (clients.go)
	events  *eventStream // the event stream (events.go)

	notify *notifier   // d.mu
	bg     *background // d.mu (background.go)

	settingsOnce sync.Once
	settingsFile *config.File // config.toml (settings.go)

	healthy  healthState // the setup checks' last word (health.go)
	speaking speechState // reading aloud (speech.go)
	updates  updateState // the newest release (update.go)

	agentSince map[string]time.Time // front.go: since when an agent without hooks is in front
	agentSeen  map[string]uint64    // agentstate.go: the last OSC 7501 report applied, per session

	wake waking // wake.go

	remoteHooks remoteHooks    // remote.go
	remote      *host.Host     // Hosting (hosting.go)
	client      *client.Client // Remote hosts: sessions added from them (remotehosts.go)
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
	if cfg.HostAgent == nil {
		cfg.HostAgent = func(id string) (term.AgentState, bool) { return term.AgentStatus(cfg.Paths.HostSocket(id)) }
	}
	if cfg.HostScreen == nil {
		cfg.HostScreen = func(id string) (string, bool) {
			text, err := term.ScreenText(cfg.Paths.HostSocket(id))
			return text, err == nil
		}
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
	d := &Daemon{cfg: cfg, store: st, ln: ln, stop: make(chan struct{}), bg: newBackground(), events: newEventStream()}
	sweepHosts(cfg.Paths)
	_ = os.Remove(cfg.Paths.OldPopupLock())
	if cfg.Notify != nil {
		d.notify = newNotifier(*cfg.Notify)
	}
	if cfg.OnRemoteChanged != nil {
		d.OnRemoteChanged(cfg.OnRemoteChanged)
	}
	d.mu.Lock()
	d.refreshNotify()
	d.mu.Unlock()
	defer os.Remove(sock)
	if err := d.loadClients(ctx); err != nil {
		return err
	}
	if err := d.moveRemoteData(ctx); err != nil {
		cfg.Log.Printf("config: %v", err)
	}
	d.startRemote()
	defer d.remote.Close()
	d.startRemoteHosts(ctx)
	defer d.client.Close()
	d.startSettings(ctx)
	d.startHealth(ctx)
	d.startUpdates(ctx)

	if err := d.drainSpool(ctx); err != nil {
		cfg.Log.Printf("drain spool: %v", err)
	}
	cfg.Log.Printf("listening on %s (version %s, pid %d)", sock, cfg.Version, os.Getpid())
	go d.background(ctx)
	go d.scheduler(ctx)
	go d.keepForgetting(ctx)
	go d.followEvents(ctx)

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
	if err := peer.Check(c); err != nil {
		d.cfg.Log.Printf("rejected connection: %v", err)
		return
	}
	r := bufio.NewReaderSize(c, 64*1024)
	who := &caller{local: true} // the user on this machine (design 9.1)
	who.pid, _ = peer.PID(c)
	ctx = context.WithValue(ctx, callerKey{}, who)
	for {
		var req ipc.Request
		if err := ipc.ReadJSON(r, &req); err != nil {
			return
		}
		if req.Op == ipc.OpHello && who.clientID == "" {
			defer d.hello(ctx, helloOf(req.Data), who)()
		}
		if req.Op == ipc.OpSubscribe {
			d.serveEvents(ctx, c, r, req)
			return
		}
		data, err := d.dispatch(ctx, req)
		resp := ipc.Response{OK: err == nil}
		if err != nil {
			// Every error has its code: a client decides from it, never from
			// the message.
			r := resultOf("", err)
			resp.Error, resp.Code = r.Error, r.Code
		} else if data != nil {
			b, merr := json.Marshal(onTheWire(data))
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
	if id := cmp.Or(req.RequestID, ipc.RequestIDOf(ctx)); id != "" {
		if name, ok := retryable(req); ok {
			return d.requests.once(ctx, d, id, name, req)
		}
	}
	return d.run(ctx, req)
}

// run runs one op.
func (d *Daemon) run(ctx context.Context, req ipc.Request) (any, error) {
	if out, done, err := d.forwardToHost(ctx, req); done { // a host's: it makes the change
		return out, err
	}
	if isJobOp(req.Op) {
		return d.handleJob(ctx, req)
	}
	if isSpeechOp(req.Op) {
		return d.handleSpeech(ctx, req)
	}
	if isFolderOp(req.Op) {
		return d.handleFolders(ctx, req)
	}
	if isHealthOp(req.Op) {
		return d.handleHealth(ctx, req)
	}
	if isConfigOp(req.Op) {
		return d.handleConfig(ctx, req)
	}
	if isRemoteHostsOp(req.Op) {
		return d.handleRemoteHosts(ctx, req)
	}
	if isRemoteOp(req.Op) {
		return d.handleRemote(req)
	}
	switch req.Op {
	case ipc.OpHello:
		return d.helloReply(req.Data)
	case ipc.OpPing, ipc.OpShutdown:
		return nil, nil
	case ipc.OpEvent:
		var ev ipc.HookEvent
		if err := json.Unmarshal(req.Data, &ev); err != nil {
			return nil, badRequest("bad event: %v", err)
		}
		return nil, d.ApplyHook(ctx, ev)
	case ipc.OpHarnesses:
		return d.harnesses(), nil
	case ipc.OpClients, ipc.OpClientPresence, ipc.OpNoticeGet, ipc.OpClientProfile, ipc.OpClientProfileSet, ipc.OpKeysSet, ipc.OpKeys:
		return d.handleClients(ctx, req)
	case ipc.OpSessionScreen:
		r, err := decode[ipc.ScreenRequest](req.Data)
		if err != nil {
			return nil, err
		}
		return d.screen(ctx, r)
	case ipc.OpSnapshot:
		var sr ipc.SnapshotRequest
		if len(req.Data) > 0 {
			var err error
			if sr, err = decode[ipc.SnapshotRequest](req.Data); err != nil {
				return nil, err
			}
		}
		return d.snapshot(ctx, cmp.Or(sr.ClientID, callerOf(ctx).clientID))
	case ipc.OpMutate:
		var m ipc.Mutation
		if err := json.Unmarshal(req.Data, &m); err != nil {
			return nil, badRequest("bad mutation: %v", err)
		}
		return d.routeMutation(ctx, m)
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
		var p ipc.Page
		if len(req.Data) > 0 {
			var err error
			if p, err = decode[ipc.Page](req.Data); err != nil {
				return nil, err
			}
		}
		all, err := d.conversations(ctx)
		if err != nil {
			return nil, err
		}
		items, next, err := ipc.PageOf(all, p)
		if err != nil {
			return nil, badRequest("%v", err)
		}
		return ipc.ConversationPage{Items: items, NextCursor: next}, nil
	case ipc.OpInbox:
		r, err := decode[ipc.InboxRequest](req.Data)
		if err != nil {
			return nil, err
		}
		if r.Host != "" { // a host's, as its tree has it
			items, next, err := ipc.PageOf(d.client.SessionInbox(r.Host, r.SessionID), r.Page)
			if err != nil {
				return nil, badRequest("%v", err)
			}
			return ipc.InboxPage{Items: items, NextCursor: next}, nil
		}
		return d.inboxHistory(ctx, r)
	case ipc.OpInboxAnswer:
		a, err := decode[ipc.InboxAnswer](req.Data)
		if err != nil {
			return nil, err
		}
		return nil, d.answerInbox(ctx, a)
	case ipc.OpLayout:
		var l ipc.Layout
		if len(req.Data) > 0 {
			if err := json.Unmarshal(req.Data, &l); err != nil {
				return nil, badRequest("bad layout request: %v", err)
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		value, err := d.store.AppState(ctx, layoutKeyFor(l.ClientID))
		return ipc.Layout{ClientID: l.ClientID, JSON: value}, err
	case ipc.OpSetLayout:
		l, err := decode[ipc.Layout](req.Data)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.store.SetAppState(ctx, layoutKeyFor(l.ClientID), l.JSON)
	case ipc.OpResume:
		m, err := decode[ipc.Mutation](req.Data)
		if err != nil {
			return nil, err
		}
		if len(m.Targets) != 1 {
			return nil, badRequest("resume takes one session")
		}
		if t := m.Targets[0]; t.Host != "" { // the host resumes its own
			return d.callHost(ctx, t.Host, ipc.OpResume, mustJSON(ipc.Mutation{Targets: ipc.Here(t.ID)}))
		}
		return d.resume(ctx, m.Targets[0].ID)
	case ipc.OpStatus:
		sessions, err := d.store.Sessions(ctx, true)
		if err != nil {
			return nil, err
		}
		events, err := d.store.EventCount(ctx)
		if err != nil {
			return nil, err
		}
		return ipc.Status{Version: d.cfg.Version, Pid: os.Getpid(), Sessions: len(sessions), Events: events,
			RemoteSessions: remoteCount(sessions)}, nil
	default:
		return nil, &codedError{code: ipc.CodeUnknownOp, err: fmt.Errorf("unknown op %q", req.Op)}
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
	case core.IsAntigravityHook(ev.Name):
		// agy only reports for the session it runs in; a row of its own would
		// be taken for a Claude conversation.
		return nil
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
	// The session, its Inbox and the event log change together, or not at all:
	// a crash in between would leave a status and an Inbox that disagree.
	err = d.store.WithTx(ctx, func(st *store.Store) error {
		if isNew {
			if err := st.CreateSession(ctx, sess); err != nil {
				return err
			}
		} else if tr.Applied {
			if err := st.UpdateSession(ctx, sess); err != nil {
				return err
			}
		}
		if tr.Applied { // not a stale event
			if err := applyInbox(ctx, st, sess, e); err != nil {
				return err
			}
		}
		return st.AppendEvent(ctx, store.LoggedEvent{
			At: e.Time, SessionID: sess.ID, ClaudeSessionID: e.SessionID, Kind: string(e.Kind),
			Detail: string(sess.Status), ToolName: e.ToolName,
		})
	})
	if err != nil {
		return err
	}
	d.transition(sess, tr) // after the commit: a notice for a change that was saved
	return nil
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
// stops. Both take d.mu, like hook handling, so updates never interleave. No
// subprocess runs while d.mu is held: a hook waits for it, and gives up after
// half a second.
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
			// A hook that timed out spools its event. Applying it now, not at the
			// next restart, keeps it fresh: core.Apply skips it if a newer event
			// has already arrived.
			if err := d.drainSpool(ctx); err != nil {
				d.cfg.Log.Printf("drain spool: %v", err)
			}
			if rec != nil {
				d.reconcile(ctx, rec)
			}
		case <-frontT.C:
			changed, err := d.frontOnce(ctx)
			if err != nil {
				d.cfg.Log.Printf("foreground: %v", err)
			}
			agentChanged, err := d.agentStateOnce(ctx)
			if err != nil {
				d.cfg.Log.Printf("agent state: %v", err)
			}
			if changed || agentChanged {
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

// reconcile runs one pass of rec. Claude's listing is fetched with d.mu
// released, and only when a session needs it; the result is applied with it held.
func (d *Daemon) reconcile(ctx context.Context, rec *reconcile.Reconciler) {
	d.mu.Lock()
	need, err := rec.NeedsListing(ctx)
	d.mu.Unlock()
	if err != nil {
		d.cfg.Log.Printf("reconcile: %v", err)
		return
	}
	var l reconcile.Listing
	if need {
		if l, err = rec.Fetch(ctx); err != nil {
			d.cfg.Log.Printf("reconcile: %v", err)
			return
		}
	}
	d.mu.Lock()
	res, err := rec.Apply(ctx, l)
	d.mu.Unlock()
	if err != nil {
		d.cfg.Log.Printf("reconcile: %v", err)
	} else if res.Discovered+res.Updated > 0 {
		d.cfg.Log.Printf("reconcile: %d discovered, %d updated", res.Discovered, res.Updated)
		d.changes.bump()
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
		if s.OnHost() || (s.Status != core.StatusBusy && s.Status != core.StatusWaiting) {
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
		s.Unseen = true
		err = d.store.WithTx(ctx, func(st *store.Store) error {
			// A permission denied or a question left in the session isn't waiting any more.
			if err := applyInbox(ctx, st, s, e); err != nil {
				return err
			}
			if err := st.UpdateSession(ctx, s); err != nil {
				return err
			}
			return st.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
				Kind: string(core.EventInterrupted), Detail: string(s.Status)})
		})
		if err != nil {
			return changed, err
		}
		changed = true
		d.transition(s, tr)
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

// onTheWire is what an op answers as clients see it: a session or group as
// its wire type (internal/ipc/wire.go), never internal/core's.
func onTheWire(data any) any {
	switch v := data.(type) {
	case *core.Session:
		return ipc.SessionOf(v)
	case core.Session:
		return ipc.SessionOf(&v)
	case []*core.Session:
		return ipc.SessionsOf(v)
	case *core.Group:
		return ipc.GroupOf(v)
	case core.Group:
		return ipc.GroupOf(&v)
	}
	return data
}
