package store

import (
	"errors"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
)

// A new store has a Claude and an Antigravity harness. Harnesses go last,
// rename and change in place, and names are unique ignoring case and "_"
// for a space.
func TestHarnesses(t *testing.T) {
	s, _ := open(t)
	hs, err := s.Harnesses(ctx)
	must(t, err)
	if len(hs) != 2 || hs[0].Name != "Claude" || hs[0].Kind != core.HarnessClaude || hs[0].ConfigDir != "" ||
		hs[1].Name != "Antigravity" || hs[1].Order != 2 {
		t.Fatalf("harnesses = %+v", hs)
	}
	work := &core.Harness{Kind: core.HarnessClaude, Name: " My Work ", ConfigDir: "~/.claude-my_work", Args: []string{"--model", "opus"}}
	must(t, s.SaveHarness(ctx, work))
	if work.ID == "" || work.Order != 3 || work.Name != "My Work" {
		t.Errorf("added = %+v", work)
	}
	got, err := s.Harness(ctx, work.ID)
	must(t, err)
	if got.ConfigDir != "~/.claude-my_work" || len(got.Args) != 2 || got.Args[1] != "opus" || got.CreatedAt.IsZero() {
		t.Errorf("read back = %+v", got)
	}
	for _, taken := range []string{"my work", "MY_WORK", "claude"} {
		if err := s.SaveHarness(ctx, &core.Harness{Kind: core.HarnessClaude, Name: taken}); !errors.Is(err, ErrHarnessTaken) {
			t.Errorf("%q: %v", taken, err)
		}
	}
	got.Name, got.Args, got.Command = "Job", nil, "/bin/claude"
	must(t, s.SaveHarness(ctx, got))
	again, _ := s.Harness(ctx, work.ID)
	if again.Name != "Job" || again.Args != nil || again.Command != "/bin/claude" || again.Order != 3 {
		t.Errorf("changed = %+v", again)
	}
	must(t, s.SaveHarness(ctx, again)) // its own name is fine
	if err := s.SaveHarness(ctx, &core.Harness{ID: "nope", Kind: core.HarnessClaude, Name: "Nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	must(t, s.RemoveHarness(ctx, work.ID))
	if _, err := s.Harness(ctx, work.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removed: %v", err)
	}
	if err := s.RemoveHarness(ctx, work.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removed twice: %v", err)
	}
}

// Migration 20 links the sessions and jobs there were to the two harnesses
// it makes, by their kind.
func TestHarnessMigrationLinksSessionsAndJobs(t *testing.T) {
	s, p := open(t)
	for _, q := range []string{
		`DROP TABLE harnesses`, `ALTER TABLE sessions DROP COLUMN harness_id`, `ALTER TABLE sessions RENAME COLUMN harness_kind TO harness`,
		`INSERT INTO sessions (id, name, cwd, status, kind, created_at, harness) VALUES ('s-agy', 'a', '/w', 'done', 'hosted', 1, 'antigravity'),
			('s-claude', 'c', '/w', 'done', 'hosted', 1, 'claude'), ('s-term', 't', '/w', 'done', 'hosted', 1, 'terminal')`,
		`INSERT INTO jobs (id, spec, cron, created_at, updated_at, last_due) VALUES
			('j-agy', '{"name":"a","harness":"antigravity","where":"folder"}', '0 9 * * *', 1, 1, 1),
			('j-claude', '{"name":"c","where":"folder"}', '0 9 * * *', 1, 1, 1)`,
		`ALTER TABLE sessions ADD COLUMN bg_id TEXT NOT NULL DEFAULT ''`, `ALTER TABLE sessions ADD COLUMN external TEXT NOT NULL DEFAULT ''`, `PRAGMA user_version = 19`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	must(t, s.Close())
	s2, err := Open(ctx, p)
	must(t, err)
	defer s2.Close()
	hs, err := s2.Harnesses(ctx)
	must(t, err)
	if len(hs) != 2 {
		t.Fatalf("harnesses = %+v", hs)
	}
	claude, agy := hs[0].ID, hs[1].ID
	for id, want := range map[string][2]string{"s-agy": {"antigravity", agy}, "s-claude": {"claude", claude}, "s-term": {"terminal", ""}} {
		sess, err := s2.Session(ctx, id)
		if err != nil || sess.HarnessKind != want[0] || sess.HarnessID != want[1] {
			t.Errorf("%s = %+v %v", id, sess, err)
		}
	}
	for id, want := range map[string]string{"j-agy": agy, "j-claude": claude} {
		j, err := s2.Job(ctx, id)
		if err != nil || j.HarnessID != want {
			t.Errorf("%s: %+v %v", id, j, err)
		}
	}
}
