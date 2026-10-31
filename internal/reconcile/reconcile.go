// Package reconcile cross-checks acmd's view of sessions with Claude's own
// (`claude agents --json`) and with process liveness (SPEC §5.3, §6.7):
// it discovers background sessions acmd hasn't seen hooks for, fills in
// ids, pids and names, corrects the provisional Kind, marks stopped and
// dead sessions paused, and lets Claude's `status` win when hooks and
// Claude disagree for longer than a grace period.
package reconcile

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/store"
)

// Lister lists Claude sessions (claudecli.CLI.Agents in production).
type Lister interface {
	Agents(ctx context.Context) ([]claudecli.Agent, error)
}

// Store is what the reconciler needs from the store.
type Store interface {
	Sessions(ctx context.Context, includeArchived bool) ([]*core.Session, error)
	SessionByClaudeID(ctx context.Context, claudeID string) (*core.Session, error)
	CreateSession(ctx context.Context, s *core.Session) error
	UpdateSession(ctx context.Context, s *core.Session) error
}

// Reconciler holds state between runs (disagreement timers).
type Reconciler struct {
	Store  Store
	Agents Lister
	Now    func() time.Time
	// Grace is how long hooks and Claude may disagree before Claude wins.
	Grace time.Duration
	// StartupGrace protects freshly created sessions that Claude hasn't
	// listed yet from being marked paused.
	StartupGrace time.Duration
	// Alive reports whether a pid is running (default: kill(pid, 0)).
	Alive func(pid int) bool

	disagreeSince map[string]time.Time
}

// Result summarizes one run.
type Result struct {
	Discovered, Updated int
}

// Once runs one reconciliation pass.
func (r *Reconciler) Once(ctx context.Context) (Result, error) {
	r.defaults()
	var res Result
	agents, err := r.Agents.Agents(ctx)
	if err != nil {
		return res, err
	}
	bySession := make(map[string]claudecli.Agent, len(agents))
	for _, a := range agents {
		if a.SessionID != "" {
			bySession[a.SessionID] = a
		}
	}

	sessions, err := r.Store.Sessions(ctx, false)
	if err != nil {
		return res, err
	}
	known := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		known[s.ClaudeSessionID] = true
		for _, h := range s.ClaudeHistory {
			known[h] = true
		}
		if r.reconcileSession(s, bySession) {
			if err := r.Store.UpdateSession(ctx, s); err != nil {
				return res, err
			}
			res.Updated++
		}
	}

	// Background sessions acmd has no row for (started elsewhere, or hooks
	// were lost): create them.
	for _, a := range agents {
		if !a.Background() || a.SessionID == "" || known[a.SessionID] {
			continue
		}
		if _, err := r.Store.SessionByClaudeID(ctx, a.SessionID); err == nil {
			continue // archived session we didn't list
		} else if !errors.Is(err, store.ErrNotFound) {
			return res, err
		}
		s := &core.Session{
			ClaudeSessionID: a.SessionID, BgID: a.ID, Kind: core.KindBackground, Cwd: a.Cwd,
			Name: a.Name, NameSource: core.NameAuto, CreatedAt: r.Now().UTC(),
		}
		if a.Pid != nil {
			s.Pid = *a.Pid
		}
		core.Apply(s, core.Event{Kind: core.EventAgentStatus, AgentStatus: a.StatusString(), Time: r.Now()})
		if err := r.Store.CreateSession(ctx, s); err != nil {
			return res, err
		}
		res.Discovered++
	}
	return res, nil
}

// reconcileSession updates s from Claude's listing; it reports whether s changed.
func (r *Reconciler) reconcileSession(s *core.Session, bySession map[string]claudecli.Agent) bool {
	a, listed := bySession[s.ClaudeSessionID]
	if !listed {
		return r.unlisted(s)
	}
	changed := false
	setStr := func(dst *string, v string) {
		if v != "" && *dst != v {
			*dst, changed = v, true
		}
	}
	setStr(&s.BgID, a.ID)
	setStr(&s.Cwd, a.Cwd)
	if s.NameSource != core.NameUser {
		setStr(&s.Name, a.Name)
	}
	pid := 0
	if a.Pid != nil {
		pid = *a.Pid
	}
	if s.Pid != pid {
		s.Pid, changed = pid, true
	}
	// Correct the provisional classification from the hook (SPEC §6.3).
	if a.Background() && s.Kind != core.KindBackground {
		s.Kind, s.External, changed = core.KindBackground, nil, true
	} else if a.Kind == "interactive" && s.Kind != core.KindExternal {
		s.Kind, changed = core.KindExternal, true
	}

	status := a.StatusString()
	if agrees(s.Status, status) {
		delete(r.disagreeSince, s.ID)
		return changed
	}
	if status == core.AgentStopped { // immediate: a stopped session isn't a race
		return r.apply(s, core.AgentStopped) || changed
	}
	since, ok := r.disagreeSince[s.ID]
	if !ok {
		r.disagreeSince[s.ID] = r.Now()
		return changed
	}
	if r.Now().Sub(since) < r.Grace {
		return changed
	}
	delete(r.disagreeSince, s.ID)
	return r.apply(s, status) || changed
}

// unlisted handles a live session Claude doesn't list.
func (r *Reconciler) unlisted(s *core.Session) bool {
	switch s.Status {
	case core.StatusPaused, core.StatusArchived:
		return false
	}
	if s.Kind == core.KindExternal {
		// Interactive sessions aren't always listed (e.g. child sessions,
		// M0 C5): rely on the process.
		if s.Pid > 0 && !r.Alive(s.Pid) {
			s.Pid = 0
			return r.apply(s, core.AgentStopped)
		}
		return false
	}
	if s.Status == core.StatusStarting && r.Now().Sub(s.CreatedAt) < r.StartupGrace {
		return false
	}
	s.Pid = 0
	return r.apply(s, core.AgentStopped)
}

func (r *Reconciler) apply(s *core.Session, agentStatus string) bool {
	tr := core.Apply(s, core.Event{Kind: core.EventAgentStatus, AgentStatus: agentStatus, Time: r.Now()})
	return tr.Changed()
}

// agrees reports whether acmd's status is consistent with Claude's status.
func agrees(ours core.Status, theirs string) bool {
	switch theirs {
	case core.AgentStopped:
		return ours == core.StatusPaused
	case core.AgentBusy:
		return ours == core.StatusBusy || ours == core.StatusStarting
	case core.AgentWaiting:
		return ours == core.StatusWaiting
	case core.AgentIdle:
		// Idle covers finished turns, errors and (briefly) a question being shown.
		return ours == core.StatusDone || ours == core.StatusError || ours == core.StatusWaiting
	}
	return true // unknown status values: don't fight
}

func (r *Reconciler) defaults() {
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Grace == 0 {
		r.Grace = 5 * time.Second
	}
	if r.StartupGrace == 0 {
		r.StartupGrace = 30 * time.Second
	}
	if r.Alive == nil {
		r.Alive = pidAlive
	}
	if r.disagreeSince == nil {
		r.disagreeSince = map[string]time.Time{}
	}
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
