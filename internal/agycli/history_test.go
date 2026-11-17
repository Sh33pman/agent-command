package agycli

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// history is agy 1.5.10's history.jsonl, as it writes it: slash commands with a
// type, some lines without a conversation, a /plan without one.
const history = `{"display":"/clear","timestamp":1794892156072,"workspace":"/Users/me","type":"slash_command"}
{"display":"/model","timestamp":1794892162119,"workspace":"/Users/me","conversationId":"eb01dae4-d7c6-46db-ba01-fb622a0960ba","type":"slash_command"}
{"display":"/exit","timestamp":1794893420323,"workspace":"/Users/me","conversationId":"eb01dae4-d7c6-46db-ba01-fb622a0960ba","type":"slash_command"}
{"display":"Reply with exactly the single word: ping","timestamp":1794929709247,"workspace":"/w/ws","conversationId":"ee7b48a5-5c41-4fa1-96d3-40b1ffdd9668"}
{"display":"In one short sentence, what is 2+2?","timestamp":1794929798851,"workspace":"/w/ws","conversationId":"ee7b48a5-5c41-4fa1-96d3-40b1ffdd9668"}
not json
{"display":"/plan Delete notes2.txt with rm.","timestamp":1794930440777,"workspace":"/w/ws","conversationId":"84b306a4-9e41-471f-99b0-07f3bf1ab83b"}
{"display":"Run exactly this shell command\nand tell me","timestamp":1794930490913,"workspace":"/w/other","conversationId":"84b306a4-9e41-471f-99b0-07f3bf1ab83b"}
{"display":"--evil","timestamp":1794930490999,"workspace":"/w","conversationId":"--rm -rf"}
{"display":"no folder","timestamp":1794930491000,"conversationId":"0a0a0a0a-0000-0000-0000-000000000000"}
`

func agyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestConversations(t *testing.T) {
	dir := agyDir(t)
	got, err := Conversations(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	want := []Conversation{
		{ID: "84b306a4-9e41-471f-99b0-07f3bf1ab83b", Cwd: "/w/other", Title: "Run exactly this shell command", UpdatedAt: time.UnixMilli(1794930490913).UTC()},
		{ID: "ee7b48a5-5c41-4fa1-96d3-40b1ffdd9668", Cwd: "/w/ws", Title: "Reply with exactly the single word: ping", UpdatedAt: time.UnixMilli(1794929798851).UTC()},
		{ID: "eb01dae4-d7c6-46db-ba01-fb622a0960ba", Cwd: "/Users/me", Title: "", UpdatedAt: time.UnixMilli(1794893420323).UTC()},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	if got, _ := Conversations(dir, 1); len(got) != 1 || got[0].ID != want[0].ID {
		t.Errorf("limit: %+v", got)
	}
	if got, err := Conversations(t.TempDir(), 10); err != nil || len(got) != 0 {
		t.Errorf("no history: %+v %v", got, err)
	}
}

// agy's own titles win over the first prompt. Its database is only read: no
// journal or lock files appear next to it.
func TestConversationTitles(t *testing.T) {
	dir := agyDir(t)
	path := filepath.Join(dir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\",`preview` text NOT NULL DEFAULT \"\"," +
			"`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL, PRIMARY KEY (`conversation_id`))",
		"INSERT INTO conversation_summaries VALUES ('ee7b48a5-5c41-4fa1-96d3-40b1ffdd9668', 'Single Word Reply Request', '', '2026-11-17', '')",
		"INSERT INTO conversation_summaries VALUES ('eb01dae4-d7c6-46db-ba01-fb622a0960ba', '', '', '2026-11-17', '')",
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	must(t, db.Close())
	before := files(t, dir)

	got, err := Conversations(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Title != "Single Word Reply Request" || got[0].Title != "Run exactly this shell command" || got[2].Title != "" {
		t.Errorf("titles: %+v", got)
	}
	if after := files(t, dir); after != before {
		t.Errorf("files changed: %s -> %s", before, after)
	}

	// A broken database is no titles, not an error.
	must(t, os.WriteFile(path, []byte("not a database"), 0o600))
	if got, err := Conversations(dir, 10); err != nil || got[1].Title != "Reply with exactly the single word: ping" {
		t.Errorf("broken database: %+v %v", got, err)
	}
}

func TestFindConversation(t *testing.T) {
	dir := agyDir(t)
	c, err := FindConversation(dir, " EE7B48A5 ")
	if err != nil || c.ID != "ee7b48a5-5c41-4fa1-96d3-40b1ffdd9668" || c.Cwd != "/w/ws" {
		t.Errorf("prefix: %+v %v", c, err)
	}
	if _, err := FindConversation(dir, "ee7b"); !errors.Is(err, ErrNoConversation) || !strings.Contains(err.Error(), "8 characters") {
		t.Errorf("short: %v", err)
	}
	if _, err := FindConversation(dir, "ffffffff"); !errors.Is(err, ErrNoConversation) {
		t.Errorf("none: %v", err)
	}
	two := history + `{"display":"x","timestamp":1,"workspace":"/w","conversationId":"ee7b48a5-0000-4000-8000-000000000000"}` + "\n"
	must(t, os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(two), 0o600))
	if _, err := FindConversation(dir, "ee7b48a5"); err == nil || !strings.Contains(err.Error(), "2 Antigravity conversations") {
		t.Errorf("ambiguous: %v", err)
	}
	if Dir("/h") != "/h/.gemini/antigravity-cli" {
		t.Error(Dir("/h"))
	}
}

// A line over the limit is skipped, and the ones after it still read.
func TestLongLine(t *testing.T) {
	dir := t.TempDir()
	long := `{"display":"` + strings.Repeat("x", maxLine) + `","timestamp":1,"workspace":"/w","conversationId":"11111111-1111-4111-8111-111111111111"}`
	ok := `{"display":"hi","timestamp":2,"workspace":"/w","conversationId":"22222222-2222-4222-8222-222222222222"}`
	must(t, os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(long+"\n"+ok), 0o600))
	got, err := Conversations(dir, 10)
	if err != nil || len(got) != 1 || got[0].Title != "hi" {
		t.Errorf("got %+v %v", got, err)
	}
}

func files(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
