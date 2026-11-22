package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// With --json, a command prints the answer of the op it ran, as the daemon
// sent it, and none of its text.
func TestJSONIsTheOpsAnswer(t *testing.T) {
	f := newFakeDaemon(t)
	sessions := ipc.SessionsOf([]*core.Session{
		{ID: "s1", Name: "api", Kind: core.KindHosted, Status: core.StatusPaused, GroupID: "default"},
		{ID: "s2", Name: "old", Kind: core.KindHosted, Status: core.StatusArchived, GroupID: "default"},
	})
	job := &core.Job{ID: "j1", JobSpec: core.JobSpec{Name: "nightly", Schedule: "0 2 * * *", Where: core.WhereFolder, Cwd: "/w"}}
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Sessions: sessions, Groups: ipc.GroupsOf([]*core.Group{{ID: "default", Name: "Default", IsDefault: true}}),
		Jobs: []*core.Job{job}})
	results := ipc.Results{Results: []ipc.Result{{ID: "s1", OK: true}}}
	f.reply(ipc.OpMutate, results)
	resumed := ipc.SessionOf(&core.Session{ID: "s1", Name: "api", Status: core.StatusBusy})
	f.reply(ipc.OpResume, resumed)
	screen := ipc.Screen{ID: "s1", Text: "$ ", Lines: 1, Total: 1}
	f.reply(ipc.OpSessionScreen, screen)
	run := core.JobRun{ID: 4, JobID: "j1", Result: "started"}
	f.reply(ipc.OpJobRun, run)
	f.reply(ipc.OpJobRuns, []core.JobRun{run})
	f.reply(ipc.OpJobPause, nil)
	cfg := ipc.ConfigState{Values: map[string]any{"default_harness": "claude"}}
	f.reply(ipc.OpConfigGet, cfg)
	f.reply(ipc.OpConfigSet, cfg)

	compact := func(v any) string {
		b, err := json.Marshal(v)
		must(t, err)
		return string(b)
	}
	same := func(out, want string) bool {
		var a, b any
		return json.Unmarshal([]byte(out), &a) == nil && json.Unmarshal([]byte(want), &b) == nil && compact(a) == compact(b)
	}
	for _, c := range []struct {
		args []string
		want any
	}{
		{[]string{"rename", "s1", "--to", "x"}, results},
		{[]string{"describe", "s1", "fixes"}, results},
		{[]string{"archive", "s1", "--yes"}, results},
		{[]string{"finish", "s1", "--yes"}, results},
		{[]string{"move", "s1", "Default"}, results},
		{[]string{"sort", "name"}, results},
		{[]string{"resume", "s1"}, resumed},
		{[]string{"screen", "s1"}, screen},
		{[]string{"schedule", "run", "nightly"}, run},
		{[]string{"schedule", "runs", "nightly"}, []core.JobRun{run}},
		{[]string{"schedule", "pause", "nightly"}, map[string]any{}},
		{[]string{"schedule", "list"}, []*core.Job{job}},
		{[]string{"schedule", "show", "nightly"}, job},
		{[]string{"config", "show"}, cfg},
		{[]string{"config", "set", "default_harness", "claude"}, cfg},
		{[]string{"list"}, sessions[:1]},
		{[]string{"group", "list"}, ipc.GroupsOf([]*core.Group{{ID: "default", Name: "Default", IsDefault: true}})},
	} {
		out := mustRun(t, append(c.args, "--json")...)
		if !same(out, compact(c.want)) {
			t.Errorf("acmd %s --json = %s, want the op's answer %s", strings.Join(c.args, " "), out, compact(c.want))
		}
	}
}

// Errors: a code and a message as JSON on stderr, and an exit code for the
// kind. What changed before a failure is still printed.
func TestJSONErrorsAndExitCodes(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Sessions: ipc.SessionsOf([]*core.Session{
		{ID: "s1", Name: "a", Kind: core.KindHosted, Status: core.StatusDone}, {ID: "s2", Name: "b", Kind: core.KindHosted, Status: core.StatusDone}})})
	partial := ipc.Results{Results: []ipc.Result{{ID: "s1", OK: true}, {ID: "s2", Code: ipc.CodeHostUnreachable, Error: "studio is gone"}}}
	f.reply(ipc.OpMutate, partial)
	var out, errOut bytes.Buffer
	err := Execute("acmd", []string{"archive", "s1", "s2", "--yes", "--json"}, &out, &errOut)
	if ExitCode(err) != exitPartial || !strings.Contains(out.String(), `"host_unreachable"`) {
		t.Errorf("partial: %d %v\n%s", ExitCode(err), err, out.String())
	}
	errOut.Reset()
	WriteError(&errOut, err)
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if json.Unmarshal(errOut.Bytes(), &e) != nil || e.Error.Code != "partial" || e.Error.Message != "1 of 2 didn't change" {
		t.Errorf("JSON error = %s", errOut.String())
	}

	err = Execute("acmd", []string{"screen", "nope", "--json"}, &out, &errOut)
	if ExitCode(err) != exitFailed {
		t.Errorf("no such session: %d %v", ExitCode(err), err)
	}
	err = Execute("acmd", []string{"rename", "--json"}, &out, &errOut)
	if ExitCode(err) != exitUsage {
		t.Errorf("no arguments: %d %v", ExitCode(err), err)
	}
	if err := Execute("acmd", []string{"list", "--nope"}, &out, &errOut); ExitCode(err) != exitUsage {
		t.Errorf("a bad flag: %d %v", ExitCode(err), err)
	}
	for code, exit := range map[string]int{ipc.CodeNotFound: exitNotFound, ipc.CodeInvalid: exitInvalid, "forbidden": exitNotSupported,
		ipc.CodeNotRunning: exitNotRunning, ipc.CodeHostUnreachable: exitUnreachable, ipc.CodeConflict: exitConflict,
		ipc.CodePermission: exitNotSupported, ipc.CodeBadRequest: exitInvalid, "something new": exitFailed} {
		re := &ipc.RemoteError{Msg: "x", Code: code}
		if ExitCode(re) != exit {
			t.Errorf("%s exits %d, want %d", code, ExitCode(re), exit)
		}
		if code != "something new" && errorCode(re) != code {
			t.Errorf("%s's JSON code is %s", code, errorCode(re))
		}
	}
	jsonOut = false
	errOut.Reset()
	WriteError(&errOut, errors.New("plain"))
	if errOut.String() != "error: plain\n" {
		t.Errorf("text error = %q", errOut.String())
	}
}
