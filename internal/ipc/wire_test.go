package ipc

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/schedule"
)

var update = flag.Bool("update", false, "rewrite testdata/wire")

var at = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// sampleSnapshot has a bit of everything clients show, as the daemon sends it.
func sampleSnapshot() Snapshot {
	sessions := []*core.Session{
		{ID: "s1", Name: "api", Description: "fix the login bug", GroupID: "g1", NameSource: core.NameAuto, Cwd: "/w/api",
			ClaudeSessionID: "c-1", Status: core.StatusWaiting, StatusDetail: "Allow Bash?", Unseen: true, Kind: core.KindHosted,
			HarnessKind: core.HarnessClaude, HarnessID: "h-work", Pid: 4242, Remote: true, CreatedAt: at, LastEventAt: at.Add(time.Minute), StatusSince: at.Add(time.Minute)},
		{ID: "s2", Name: "docs", GroupID: "g1", Cwd: "/w/docs", Status: core.StatusBusy, Kind: core.KindHosted, HarnessKind: core.HarnessClaude, HarnessID: "h-gone",
			SetID: "set1", SetOrder: 2, CreatedAt: at},
		{ID: "s3", Name: "shell", GroupID: "default", Cwd: "/w", Status: core.StatusDone, Kind: core.KindHosted, HarnessKind: core.HarnessTerminal,
			External: &core.PaneLoc{Terminal: "kitty", KittySocket: "unix:/tmp/kitty", KittyWindow: 7}, CreatedAt: at, ArchivedAt: at.Add(time.Hour)},
		{ID: "s4", Name: "build", GroupID: "default", Cwd: "/r/build", Status: core.StatusDone, Kind: core.KindHosted, HarnessKind: core.HarnessClaude,
			Host: "studio", RemoteID: "r9", JobID: "j1", CreatedAt: at},
	}
	groups := []*core.Group{
		{ID: "g1", Name: "Backend", Color: "blue", SortKey: 10, CreatedAt: at, Collapsed: true},
		{ID: "default", Name: "Default", IsDefault: true, SortKey: 99, CreatedAt: at},
	}
	job := &core.Job{ID: "j1", JobSpec: core.JobSpec{Name: "nightly", GroupID: "g1", Schedule: "0 2 * * *", Prompt: "Run the tests",
		Where: core.WhereFolder, Cwd: "/w/api"}, Cron: "0 2 * * *", Summary: "Every day at 2:00", CreatedAt: at, UpdatedAt: at,
		NextRun: at.Add(17 * time.Hour), LastRun: &core.JobRun{ID: 3, JobID: "j1", Due: at.Add(-7 * time.Hour), At: at.Add(-7 * time.Hour),
			Result: "started", SessionID: "s4"}, Form: &schedule.Form{Kind: schedule.FormDaily, Hour: 2}}
	// A type no client knows yet (design 7.2): a folder in g1, holding a
	// session that names only its parent. Every client draws s5 in g1.
	folder := json.RawMessage(`{"type":"folder","id":"f1","parent":"g1","name":"api","path":"/w/api"}`)
	inFolder := &Session{Type: EntitySession, ID: "s5", Name: "lint", Parent: "f1", Cwd: "/w/api", Status: core.StatusDone,
		Kind: core.KindHosted, HarnessKind: core.HarnessTerminal, CreatedAt: at}
	return Snapshot{
		Sessions:  append(SessionsOf(sessions), inFolder),
		Groups:    GroupsOf(groups),
		Sets:      []Set{{Type: EntitySet, ID: "set1", Parent: "g1"}},
		Others:    []json.RawMessage{folder},
		ViewState: ViewStateOf(core.ViewState{Mode: "groups", Sort: "activity", Desc: true, Collapsed: []string{"g1"}}),
		Inbox: []core.InboxItem{{ID: 1, SessionID: "s1", Kind: "permission", Text: "Allow Bash: make test?", Options: []string{"Yes", "No"},
			At: at.Add(time.Minute), State: "open"}},
		Jobs: []*core.Job{job},
		Harnesses: []Harness{
			{Type: EntityHarness, ID: "h-claude", Name: "Claude", Kind: core.HarnessClaude, Title: "Claude", Icon: "claude", Order: 1, Installed: true, Folder: "/Users/me/.claude"},
			{Type: EntityHarness, ID: "h-work", Name: "Work", Kind: core.HarnessClaude, Title: "Claude", Icon: "claude", Order: 2, Installed: true,
				Args: []string{"--model", "opus"}, ConfigDir: "~/.claude-work", Folder: "/Users/me/.claude-work"},
			{Type: EntityHarness, ID: "h-agy", Name: "Antigravity", Kind: core.HarnessAntigravity, Title: "Antigravity", Icon: "antigravity", Order: 3},
		},
		Settings: map[string]any{"speech.enabled": true, "remote.port": 7443},
		Health:   []Health{{ID: "hooks", Severity: HealthWarning, Title: "Claude's hooks aren't installed", Fix: &HealthFix{Op: OpInit}}},
		Features: &Features{Host: HostFeatures{OS: "darwin", HarnessKinds: []string{"claude", "terminal"}, Sessions: true, Wake: true}, Device: DeviceFeatures{Speech: true}},
		Speech:   &SpeechState{Supported: true, Enabled: true, Installed: true, Voice: "af_bella"},
	}
}

// fixtures are what every client's tests decode: the daemon's answers as they
// go over the wire.
func fixtures() map[string]any {
	snap := sampleSnapshot()
	return map[string]any{
		"snapshot.json": snap,
		"session.json":  snap.Sessions[0],
		"results.json": Results{Results: []Result{{ID: "s1", OK: true}, {ID: "s4", Code: CodeHostUnreachable, Error: "studio isn't reachable"},
			{ID: "nope", Code: CodeNotFound, Error: `no session "nope"`}}},
		"screen.json": Screen{ID: "s1", Text: "$ make test\nok", Lines: 2, Total: 40},
		"error.json":  Response{Error: "build runs on studio, which can't send its screen yet", Code: CodeNotSupported},
		// The daemon's hello: the version agreed, its range, and its ops.
		"hello.json": Hello{Proto: 2, ProtoMin: 2, ProtoMax: 3, Version: "0.82.0", Pid: 42,
			Ops: []string{OpHello, OpSnapshot, OpSubscribe, OpMutate}},
	}
}

// The fixtures in testdata/wire are what the daemon sends; go test
// ./internal/ipc -update rewrites them after a change on purpose.
func TestWireFixtures(t *testing.T) {
	for name, v := range fixtures() {
		got, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "wire", name)
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (go test ./internal/ipc -update writes it)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s changed: the protocol changed. If that's meant, run go test ./internal/ipc -update and update every client.\n%s", name, got)
		}
		checkNames(t, name, got)
	}
}

var snake = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// checkNames: every name is snake_case, no time is Go's zero, and nothing is
// null. Settings and pre_focus are keyed by setting and group, not names.
func checkNames(t *testing.T, file string, b []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if !snake.MatchString(k) {
					t.Errorf("%s: %s.%s isn't snake_case", file, path, k)
				}
				if k == "settings" || k == "pre_focus" {
					continue
				}
				walk(path+"."+k, e)
			}
		case []any:
			for _, e := range x {
				walk(path+"[]", e)
			}
		case string:
			if len(x) > 4 && x[:5] == "0001-" {
				t.Errorf("%s: %s is Go's zero time; leave it out", file, path)
			}
		case nil:
			t.Errorf("%s: %s is null; leave it out", file, path)
		}
	}
	walk("", v)
}

func TestSessionOnTheWire(t *testing.T) {
	snap := sampleSnapshot()
	waiting, inSet, archived, onHost := snap.Sessions[0], snap.Sessions[1], snap.Sessions[2], snap.Sessions[3]
	if waiting.Parent != "g1" || waiting.Order != 0 || inSet.Parent != "set1" || inSet.Order != 2 || inSet.GroupID != "g1" {
		t.Errorf("parents: %+v %+v", waiting, inSet)
	}
	if waiting.Attention == nil || waiting.Attention.Reason != core.NeedsWaiting || !waiting.Attention.Since.Equal(at.Add(time.Minute)) || inSet.Attention != nil {
		t.Errorf("attention: %+v %+v", waiting.Attention, inSet.Attention)
	}
	if waiting.Agent != core.AgentClaude || waiting.DisplayName != "api" || !slices.Contains(waiting.Capabilities, core.CanAttach) {
		t.Errorf("what it is: %+v", waiting)
	}
	if archived.External == nil || archived.External.KittyWindow != 7 || onHost.Host != "studio" {
		t.Errorf("pane and host: %+v %+v", archived, onHost)
	}
	if SessionOf(nil) != nil || (*Session)(nil).Core() != nil || (*PaneLoc)(nil).Core() != nil {
		t.Error("nil")
	}
}

// What the CLI makes of the snapshot is the daemon's sessions, groups and view.
func TestTheSnapshotBackAsCore(t *testing.T) {
	snap := sampleSnapshot()
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Sessions, snap.Sessions) || !reflect.DeepEqual(back.Groups, snap.Groups) || !reflect.DeepEqual(back.ViewState, snap.ViewState) {
		t.Errorf("round trip:\n%+v\n%+v", back.Sessions[0], snap.Sessions[0])
	}
	ss := back.CoreSessions()
	if ss[1].SetID != "set1" || ss[1].SetOrder != 2 || ss[0].SetID != "" || ss[2].External.Terminal != "kitty" || ss[0].Status != core.StatusWaiting {
		t.Errorf("sessions: %+v %+v", ss[0], ss[1])
	}
	gs := back.CoreGroups()
	if !gs[0].Collapsed || gs[0].SortKey != 10 || gs[1].Collapsed || !gs[1].IsDefault {
		t.Errorf("groups: %+v %+v", gs[0], gs[1])
	}
	if back.Groups[0].Type != "group" || back.Groups[1].Order != 0 {
		t.Errorf("group wire: %+v", back.Groups)
	}
	if vs := back.ViewState.Core(); vs.Sort != "activity" || !vs.Desc || vs.Collapsed[0] != "g1" {
		t.Errorf("view: %+v", vs)
	}
}
