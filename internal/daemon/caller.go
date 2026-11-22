package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/proc"
	"github.com/brettmostert/agent-command/internal/store"
)

// Who is on the socket: the client its hello names, the process (the peer's
// pid), and, from that process's parents, the acmd session it runs in and
// any agent (claude, agy) that started it. On this machine these are
// guardrails, not protection (design 9.1): a process can leave its parents.

// agents are the programs whose children count as an agent's.
var agents = []string{core.HarnessClaude, "agy"}

// lineage is what a process's parents say about it.
type lineage struct {
	session string // the acmd session it runs in
	agent   string // the agent that started it
}

// lineageOf walks pid's parents.
func (d *Daemon) lineageOf(pid int) lineage {
	var l lineage
	lookup := d.cfg.ProcInfo
	if lookup == nil {
		lookup = proc.Lookup
	}
	for seen := 0; pid > 1 && seen < 64; seen++ {
		p, err := lookup(pid)
		if err != nil {
			break
		}
		if l.agent == "" {
			for _, a := range agents {
				if p.Comm == a || len(p.Args) > 0 && filepath.Base(p.Args[0]) == a {
					l.agent = a
				}
			}
		}
		// A session's host: acmd host serve --session <id>.
		if i := slices.Index(p.Args, "--session"); l.session == "" && i >= 0 && i+1 < len(p.Args) && slices.Contains(p.Args, "serve") {
			l.session = p.Args[i+1]
		}
		if p.PPid == pid {
			break
		}
		pid = p.PPid
	}
	return l
}

// procOf is the caller's lineage, worked out once per connection.
func (d *Daemon) procOf(who *caller) lineage {
	who.once.Do(func() {
		if who.pid > 0 {
			who.line = d.lineageOf(who.pid)
		}
	})
	return who.line
}

// String names the caller for the event log: "client=mac-1 pid=42
// session=s1 agent=claude", or "remote=studio" for a paired device.
func (d *Daemon) describe(who *caller) string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("remote", who.remote)
	add("client", who.clientID)
	add("device", who.device)
	if who.pid > 0 {
		add("pid", fmt.Sprint(who.pid))
		l := d.procOf(who)
		add("session", l.session)
		add("agent", l.agent)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

// errAnswerRefused: inbox.permission_answers doesn't let this caller answer.
var errAnswerRefused = errors.New("refused")

// mayAnswer applies inbox.permission_answers to a permission prompt's
// answer. A paired device goes by its pairing, not this.
func (d *Daemon) mayAnswer(ctx context.Context, kind string) error {
	if kind != core.InboxPermission {
		return nil
	}
	who := callerOf(ctx)
	if who.remote != "" || who.pid == 0 {
		return nil
	}
	l := d.procOf(who)
	switch d.config().Inbox.PermissionAnswers {
	case config.AnswersAnyone:
		return nil
	case config.AnswersOutsideSessions:
		if l.session != "" {
			return &codedError{code: "forbidden", err: fmt.Errorf("%w: inbox.permission_answers is outside_sessions, and this runs in an acmd session; answer in the app, or from a terminal outside acmd", errAnswerRefused)}
		}
	default: // people
		if l.agent != "" {
			return &codedError{code: "forbidden", err: fmt.Errorf("%w: a program %s started can't answer a permission prompt (inbox.permission_answers is people); answer it yourself, in the app or the session", errAnswerRefused, l.agent)}
		}
	}
	return nil
}

// audit records who made a change in the event log.
func (d *Daemon) audit(ctx context.Context, req ipc.Request) {
	if req.Op == ipc.OpEvent {
		return // an agent's report, logged as it's applied
	}
	what := req.Op
	if req.Op == ipc.OpMutate {
		if m, err := decode[ipc.Mutation](req.Data); err == nil {
			what = m.Kind
		}
	}
	ev := store.LoggedEvent{Kind: "op", Detail: what, Caller: d.describe(callerOf(ctx))}
	if err := d.store.AppendEvent(context.WithoutCancel(ctx), ev); err != nil {
		d.cfg.Log.Printf("audit: %v", err)
	}
}
