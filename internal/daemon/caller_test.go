package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/proc"
)

// procs is a process tree: by pid, each process's parent, name and args.
type procs map[int]proc.Proc

func (p procs) lookup(pid int) (proc.Proc, error) {
	if pr, ok := p[pid]; ok {
		pr.Pid = pid
		return pr, nil
	}
	return proc.Proc{}, errors.New("no such process")
}

// tree: launchd 1; the daemon 10; a session's host 20 (acmd host serve
// --session s1) with its shell 21, Claude 22 in it, and a script 23 Claude
// ran; a terminal outside acmd 30 with its shell 31.
var tree = procs{
	10: {PPid: 1, Comm: "acmd", Args: []string{"acmd", "daemon", "run"}},
	20: {PPid: 10, Comm: "acmd", Args: []string{"/usr/local/bin/acmd", "host", "serve", "--session", "s1", "--dir", "/w"}},
	21: {PPid: 20, Comm: "zsh", Args: []string{"-zsh"}},
	22: {PPid: 21, Comm: "claude", Args: []string{"claude"}},
	23: {PPid: 22, Comm: "python3", Args: []string{"python3", "approve.py"}},
	30: {PPid: 1, Comm: "Terminal"},
	31: {PPid: 30, Comm: "zsh"},
	40: {PPid: 40, Comm: "loop"}, // its own parent
	50: {PPid: 51, Comm: "orphan"},
}

func TestLineage(t *testing.T) {
	d := &Daemon{cfg: Config{ProcInfo: tree.lookup}}
	for pid, want := range map[int]lineage{
		23: {session: "s1", agent: core.HarnessClaude}, // a script Claude ran
		21: {session: "s1"},                            // your shell in a terminal session
		31: {},                                         // a terminal outside acmd
		40: {}, 50: {}, 999: {},
	} {
		if got := d.lineageOf(pid); got != want {
			t.Errorf("pid %d: %+v, want %+v", pid, got, want)
		}
	}
	agyTree := procs{5: {PPid: 1, Comm: "node", Args: []string{"/opt/agy/bin/agy", "--mode"}}, 6: {PPid: 5, Comm: "sh"}}
	if got := (&Daemon{cfg: Config{ProcInfo: agyTree.lookup}}).lineageOf(6); got.agent != "agy" {
		t.Errorf("agy by its path: %+v", got)
	}
}

// inbox.permission_answers: people refuses a program an agent started,
// outside_sessions anything in a session, anyone nothing. Other answers and
// paired devices aren't its business.
func TestWhoMayAnswerAPermissionPrompt(t *testing.T) {
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	d.cfg.ProcInfo = tree.lookup
	ctxOf := func(c *caller) context.Context { return context.WithValue(context.Background(), callerKey{}, c) }
	script, shell, outside := ctxOf(&caller{pid: 23}), ctxOf(&caller{pid: 21}), ctxOf(&caller{pid: 31})
	remote, unknown := ctxOf(&caller{remote: "laptop", pid: 23}), context.Background()
	set := func(v string) {
		t.Helper()
		_, err := d.settings().Set(map[string]any{"inbox.permission_answers": v})
		must(t, err)
	}
	d.cfg.ConfigFile = t.TempDir() + "/config.toml"
	for _, c := range []struct {
		setting string
		ctx     context.Context
		ok      bool
	}{
		{config.AnswersPeople, script, false}, {config.AnswersPeople, shell, true}, {config.AnswersPeople, outside, true},
		{config.AnswersOutsideSessions, script, false}, {config.AnswersOutsideSessions, shell, false}, {config.AnswersOutsideSessions, outside, true},
		{config.AnswersAnyone, script, true},
		{config.AnswersPeople, remote, true}, {config.AnswersPeople, unknown, true},
	} {
		set(c.setting)
		err := d.mayAnswer(c.ctx, core.InboxPermission)
		if (err == nil) != c.ok {
			t.Errorf("%s, %v: %v", c.setting, callerOf(c.ctx), err)
		}
		if err != nil && (resultOf("", err).Code != "forbidden" || !errors.Is(err, errAnswerRefused)) {
			t.Errorf("refusal: %v", err)
		}
	}
	set(config.AnswersPeople)
	if err := d.mayAnswer(script, core.InboxQuestion); err != nil {
		t.Errorf("a question isn't a permission prompt: %v", err)
	}
}

// The event log says who made each change.
func TestChangesSayWhoMadeThem(t *testing.T) {
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	d.cfg.ProcInfo = tree.lookup
	ctx := context.WithValue(context.Background(), callerKey{}, &caller{clientID: "mac-1", pid: 23})
	d.audit(ctx, ipc.Request{Op: ipc.OpMutate, Data: []byte(`{"kind":"session.rename","id":"s1"}`)})
	d.audit(context.WithValue(context.Background(), callerKey{}, &caller{remote: "laptop"}), ipc.Request{Op: ipc.OpJobRun})
	d.audit(context.Background(), ipc.Request{Op: ipc.OpNew})
	d.audit(ctx, ipc.Request{Op: ipc.OpEvent}) // an agent's own report: logged as it's applied
	evs, err := d.store.RecentEvents(context.Background(), "", 10)
	must(t, err)
	got := []string{}
	for _, e := range evs {
		got = append(got, e.Detail+": "+e.Caller)
	}
	want := "new: unknown | job.run: remote=laptop | session.rename: client=mac-1 pid=23 session=s1 agent=claude"
	if strings.Join(got, " | ") != want {
		t.Errorf("log = %s", strings.Join(got, " | "))
	}
	if evs[0].Kind != "op" {
		t.Errorf("kind = %q", evs[0].Kind)
	}
}
