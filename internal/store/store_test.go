package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

var ctx = context.Background()

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, p, WithClock(func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

func TestOpenMigratesAndReopens(t *testing.T) {
	s, p := open(t)
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != SchemaVersion() || v < 1 {
		t.Fatalf("user_version = %d (%v), want %d", v, err, SchemaVersion())
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("db permissions = %o, want 600", perm)
	}
	g, err := s.Group(ctx, DefaultGroupID)
	if err != nil || !g.IsDefault || g.Name != "Default" {
		t.Fatalf("default group: %+v %v", g, err)
	}
	s.Close()

	// Re-opening an up-to-date database doesn't migrate or back up.
	s2, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if matches, _ := filepath.Glob(p + ".bak-*"); len(matches) != 0 {
		t.Errorf("unexpected backups: %v", matches)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	s, p := open(t)
	if _, err := s.db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, p); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v, want ErrSchemaTooNew", err)
	}
}

func TestOpenBacksUpBeforeMigrating(t *testing.T) {
	s, p := open(t)
	if err := s.CreateSession(ctx, &core.Session{Name: "before upgrade"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// A newer binary with one more migration.
	orig := migrations
	current, err := orig()
	if err != nil {
		t.Fatal(err)
	}
	base := len(current)
	t.Cleanup(func() { migrations = orig })
	migrations = func() ([]migration, error) {
		ms, err := orig()
		return append(ms, migration{version: len(ms) + 1, name: "9999_test.sql",
			sql: "ALTER TABLE sessions ADD COLUMN test_col TEXT NOT NULL DEFAULT ''"}), err
	}
	s2, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var v int
	if err := s2.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != base+1 {
		t.Errorf("user_version = %d, want %d", v, base+1)
	}
	backups, _ := filepath.Glob(fmt.Sprintf("%s.bak-v%d-*", p, base))
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one v%d backup", backups, base)
	}
	// The backup is the pre-migration database (old schema, data intact).
	migrations = orig
	b, err := Open(ctx, backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if ss, _ := b.Sessions(ctx, false); len(ss) != 1 || ss[0].Name != "before upgrade" {
		t.Errorf("backup content: %+v", ss)
	}
}

func TestOpenFailsCleanlyOnBrokenMigration(t *testing.T) {
	orig := migrations
	t.Cleanup(func() { migrations = orig })
	migrations = func() ([]migration, error) {
		ms, err := orig()
		return append(ms, migration{version: len(ms) + 1, name: "9999_broken.sql", sql: "NOT SQL"}), err
	}
	_, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err == nil || !strings.Contains(err.Error(), "9999_broken.sql") {
		t.Fatalf("err = %v, want migration failure naming the file", err)
	}
}

func TestBackupCopiesDatabase(t *testing.T) {
	s, p := open(t)
	if err := s.CreateSession(ctx, &core.Session{Name: "keep me"}); err != nil {
		t.Fatal(err)
	}
	if err := s.backup(ctx, 1); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(p + ".bak-v1-*")
	if len(matches) != 1 {
		t.Fatalf("backups = %v", matches)
	}
	b, err := Open(ctx, matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ss, err := b.Sessions(ctx, false)
	if err != nil || len(ss) != 1 || ss[0].Name != "keep me" {
		t.Fatalf("backup content: %v %v", ss, err)
	}
	if err := s.backup(ctx, 1); err == nil {
		t.Error("second backup with the same name should fail (O_EXCL), not overwrite")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s, _ := open(t)
	in := &core.Session{
		Name: "fix flaky e2e", Description: "PROJ-123", Cwd: "/w/api",
		ClaudeSessionID: "c3", ClaudeHistory: []string{"c1", "c2"}, TranscriptPath: "/t/c3.jsonl",
		Status: core.StatusWaiting, StatusDetail: "question", Unseen: true,
		Kind: core.KindExternal, BgID: "", Pid: 4242,
		External:    &core.PaneLoc{Terminal: "kitty", KittySocket: "/tmp/k", KittyWindow: 7, TTY: "ttys010"},
		LastEventAt: time.UnixMilli(1793380000123).UTC(),
	}
	if err := s.CreateSession(ctx, in); err != nil {
		t.Fatal(err)
	}
	if in.ID == "" || in.GroupID != DefaultGroupID || in.NameSource != core.NameAuto || in.CreatedAt.IsZero() {
		t.Fatalf("defaults not applied: %+v", in)
	}
	got, err := s.Session(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, in)
	}

	for _, cid := range []string{"c1", "c2", "c3"} {
		if g, err := s.SessionByClaudeID(ctx, cid); err != nil || g.ID != in.ID {
			t.Errorf("SessionByClaudeID(%s) = %v, %v", cid, g, err)
		}
	}
	if _, err := s.SessionByClaudeID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown claude id: %v", err)
	}

	// Update: new Claude id after /clear, archived.
	in.ClaudeHistory = append(in.ClaudeHistory, in.ClaudeSessionID)
	in.ClaudeSessionID = "c4"
	in.Status, in.External = core.StatusArchived, nil
	in.ArchivedAt = time.UnixMilli(1793390000000).UTC()
	if err := s.UpdateSession(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Session(ctx, in.ID)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("after update:\n got %+v\nwant %+v", got, in)
	}
	if g, err := s.SessionByClaudeID(ctx, "c4"); err != nil || g.ID != in.ID {
		t.Errorf("new claude id not indexed: %v %v", g, err)
	}

	if err := s.UpdateSession(ctx, &core.Session{ID: "missing", CreatedAt: time.Now()}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if _, err := s.Session(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}
}

func TestSessionsListAndDelete(t *testing.T) {
	s, _ := open(t)
	base := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	var ids []string
	for i, name := range []string{"a", "b", "c"} {
		sess := &core.Session{Name: name, CreatedAt: base.Add(time.Duration(i) * time.Minute)}
		if name == "b" {
			sess.ArchivedAt = base
			sess.Status = core.StatusArchived
		}
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sess.ID)
	}
	live, _ := s.Sessions(ctx, false)
	all, _ := s.Sessions(ctx, true)
	if names(live) != "a,c" || names(all) != "a,b,c" {
		t.Errorf("live=%s all=%s", names(live), names(all))
	}
	if err := s.DeleteSession(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
	if all, _ = s.Sessions(ctx, true); names(all) != "b,c" {
		t.Errorf("after delete: %s", names(all))
	}
}

func names(ss []*core.Session) string {
	var n []string
	for _, s := range ss {
		n = append(n, s.Name)
	}
	return strings.Join(n, ",")
}

func TestGroups(t *testing.T) {
	s, _ := open(t)
	g1, err := s.CreateGroup(ctx, "  PROJ-123 auth  ", "")
	if err != nil {
		t.Fatal(err)
	}
	g2, _ := s.CreateGroup(ctx, "billing", "")
	g3, _ := s.CreateGroup(ctx, "custom color", "red")
	if g1.Name != "PROJ-123 auth" || g1.Color != Palette[0] || g2.Color != Palette[1] || g3.Color != "red" {
		t.Errorf("names/colors: %q %q %q %q", g1.Name, g1.Color, g2.Color, g3.Color)
	}
	if _, err := s.CreateGroup(ctx, "proj-123 AUTH", ""); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("duplicate (case-insensitive): %v", err)
	}
	if _, err := s.CreateGroup(ctx, "  ", ""); !errors.Is(err, ErrEmptyName) {
		t.Errorf("empty name: %v", err)
	}
	if g, err := s.GroupByName(ctx, "BILLING"); err != nil || g.ID != g2.ID {
		t.Errorf("GroupByName: %v %v", g, err)
	}

	list, _ := s.Groups(ctx, false)
	var order []string
	for _, g := range list {
		order = append(order, g.Name)
	}
	if strings.Join(order, "|") != "PROJ-123 auth|billing|custom color|Default" {
		t.Errorf("order = %v (Default must be last)", order)
	}

	// Default group rules.
	for name, err := range map[string]error{
		"rename":  s.RenameGroup(ctx, DefaultGroupID, "x"),
		"delete":  s.DeleteGroup(ctx, DefaultGroupID),
		"archive": s.SetGroupArchived(ctx, DefaultGroupID, true),
	} {
		if !errors.Is(err, ErrDefaultGroup) {
			t.Errorf("%s default: %v", name, err)
		}
	}

	if err := s.RenameGroup(ctx, g2.ID, "PROJ-123 AUTH"); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("rename to duplicate: %v", err)
	}
	if err := s.RenameGroup(ctx, g2.ID, "billing v2"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupColor(ctx, g2.ID, "green"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupCollapsed(ctx, g2.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupSortKey(ctx, g2.ID, 1); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Group(ctx, g2.ID)
	if got.Name != "billing v2" || got.Color != "green" || !got.Collapsed || got.SortKey != 1 {
		t.Errorf("group after updates: %+v", got)
	}
	if err := s.SetGroupColor(ctx, "nope", "red"); !errors.Is(err, ErrNotFound) {
		t.Errorf("color missing group: %v", err)
	}
	if err := s.RenameGroup(ctx, "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing group: %v", err)
	}

	// Archive frees the name; archived groups are hidden but listable.
	if err := s.SetGroupArchived(ctx, g3.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGroup(ctx, "custom color", ""); err != nil {
		t.Errorf("name of archived group should be reusable: %v", err)
	}
	live, _ := s.Groups(ctx, false)
	all, _ := s.Groups(ctx, true)
	if len(all) != len(live)+1 {
		t.Errorf("archived listing: live=%d all=%d", len(live), len(all))
	}
}

func TestMoveAndDeleteGroupKeepsSessions(t *testing.T) {
	s, _ := open(t)
	g, _ := s.CreateGroup(ctx, "PROJ-1", "")
	a := &core.Session{Name: "a"}
	b := &core.Session{Name: "b"}
	must(t, s.CreateSession(ctx, a))
	must(t, s.CreateSession(ctx, b))
	if err := s.MoveSessions(ctx, g.ID, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Session(ctx, a.ID); got.GroupID != g.ID {
		t.Errorf("moved group = %s", got.GroupID)
	}
	if err := s.MoveSessions(ctx, "nope", a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("move to missing group: %v", err)
	}
	if err := s.MoveSessions(ctx, g.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("move missing session: %v", err)
	}
	archived, _ := s.CreateGroup(ctx, "old", "")
	must(t, s.SetGroupArchived(ctx, archived.ID, true))
	if err := s.MoveSessions(ctx, archived.ID, a.ID); err == nil {
		t.Error("move into archived group should fail")
	}

	if err := s.DeleteGroup(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		got, err := s.Session(ctx, id)
		if err != nil || got.GroupID != DefaultGroupID {
			t.Errorf("session %s after group delete: %+v %v", id, got, err)
		}
	}
	if _, err := s.Group(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted group still there: %v", err)
	}
	if err := s.DeleteGroup(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing group: %v", err)
	}
}

func TestViewsAndViewState(t *testing.T) {
	s, _ := open(t)
	if vs, err := s.ViewState(ctx); err != nil || vs.Mode != "groups" || vs.Sort != "created" {
		t.Fatalf("default view state: %+v %v", vs, err)
	}
	want := core.ViewState{Mode: "list", Sort: "activity", Desc: true, Filter: "g:proj", FocusedGroup: "g1",
		PreFocus: map[string]bool{"g2": true}, ShowArchived: true}
	if err := s.PutViewState(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ViewState(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("view state: %+v", got)
	}
}

func TestEventLogIsCappedAndMinimal(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < MaxEvents+MaxEvents/10; i++ {
		if err := s.AppendEvent(ctx, LoggedEvent{SessionID: "s", Kind: "Stop"}); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := s.EventCount(ctx)
	if n > MaxEvents {
		t.Errorf("event log has %d rows, cap %d", n, MaxEvents)
	}
	must(t, s.AppendEvent(ctx, LoggedEvent{SessionID: "x", ClaudeSessionID: "c", Kind: "PreToolUse", ToolName: "Bash", Detail: "Bash"}))
	got, err := s.RecentEvents(ctx, "x", 5)
	if err != nil || len(got) != 1 || got[0].ToolName != "Bash" || got[0].At.IsZero() {
		t.Fatalf("recent events: %+v %v", got, err)
	}
	all, _ := s.RecentEvents(ctx, "", 3)
	if len(all) != 3 || all[0].SessionID != "x" {
		t.Errorf("newest first: %+v", all)
	}
	// The schema has no column that could hold prompts or tool input.
	rows, _ := s.db.Query("SELECT name FROM pragma_table_info('events')")
	defer rows.Close()
	for rows.Next() {
		var col string
		must(t, rows.Scan(&col))
		if strings.Contains(col, "prompt") || strings.Contains(col, "input") || strings.Contains(col, "transcript") {
			t.Errorf("events has a sensitive column %q", col)
		}
	}
}

func TestNewIDIsSortable(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 26 || a >= b {
		t.Errorf("ids %s %s", a, b)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionHarness(t *testing.T) {
	s, _ := open(t)
	defer s.Close()
	agent := &core.Session{Name: "agent"}
	shell := &core.Session{Name: "shell", Harness: core.HarnessTerminal}
	for _, sess := range []*core.Session{agent, shell} {
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Session(ctx, agent.ID); err != nil || got.Harness != core.HarnessClaude {
		t.Errorf("default harness = %+v, %v", got, err)
	}
	if got, err := s.Session(ctx, shell.ID); err != nil || !got.IsTerminal() {
		t.Errorf("terminal harness = %+v, %v", got, err)
	}
}

func TestAppState(t *testing.T) {
	s, _ := open(t)
	defer s.Close()
	if got, err := s.AppState(ctx, "layout"); err != nil || got != "" {
		t.Errorf("empty = %q, %v", got, err)
	}
	for _, want := range []string{"one", "two"} {
		if err := s.SetAppState(ctx, "layout", want); err != nil {
			t.Fatal(err)
		}
		if got, err := s.AppState(ctx, "layout"); err != nil || got != want {
			t.Errorf("got %q, %v, want %q", got, err, want)
		}
	}
}

// A conversation that visits a terminal isn't the terminal's to keep.
func TestTerminalDoesNotClaimConversation(t *testing.T) {
	st, _ := open(t)
	ctx := context.Background()
	owner := &core.Session{ClaudeSessionID: "conv"}
	if err := st.CreateSession(ctx, owner); err != nil {
		t.Fatal(err)
	}
	term := &core.Session{Harness: core.HarnessTerminal, Kind: core.KindHosted}
	if err := st.CreateSession(ctx, term); err != nil {
		t.Fatal(err)
	}
	term.ClaudeSessionID = "conv"
	if err := st.UpdateSession(ctx, term); err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionByClaudeID(ctx, "conv")
	if err != nil || got.ID != owner.ID {
		t.Errorf("conv belongs to %+v, %v; want %s", got, err, owner.ID)
	}
}

// Views are gone: an upgraded database has no table for them.
func TestViewsTableDropped(t *testing.T) {
	st, _ := open(t)
	var n int
	err := st.db.QueryRowContext(context.Background(), `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'views'`).Scan(&n)
	if err != nil || n != 0 {
		t.Errorf("views tables = %d, %v", n, err)
	}
}
