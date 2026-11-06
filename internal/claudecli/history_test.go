package claudecli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	idOne = "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	idTwo = "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func writeTranscript(t *testing.T, dir, project, name string, age time.Duration, lines ...string) {
	t.Helper()
	folder := filepath.Join(dir, "projects", project)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, name+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestConversations(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "p", idOne, 2*time.Hour,
		`{"type":"user","cwd":"/w/one","gitBranch":"main"}`,
		`not json`,
		`{"type":"last-prompt","lastPrompt":"a prompt"}`,
		`{"type":"ai-title","aiTitle":"ai title"}`)
	writeTranscript(t, dir, "p", idTwo, time.Hour,
		`{"type":"user","cwd":"/w/two"}`,
		`{"type":"ai-title","aiTitle":"ai title"}`,
		`{"type":"custom-title","customTitle":"my name"}`)
	writeTranscript(t, dir, "p", "33333333-cccc-4ccc-8ccc-cccccccccccc", 0, `{"type":"mode"}`)
	writeTranscript(t, dir, "p", idOne+".orphaned-179-6d1a", 0, `{"cwd":"/w/orphan"}`)
	writeTranscript(t, dir, "p", "44444444-dddd-4ddd-8ddd-dddddddddddd", 0,
		`{"cwd":"/w/side","isSidechain":true}`)

	got, err := Conversations(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != idTwo || got[0].Title != "my name" || got[0].Cwd != "/w/two" {
		t.Errorf("newest = %+v", got[0])
	}
	if got[1].ID != idOne || got[1].Title != "ai title" || got[1].Branch != "main" {
		t.Errorf("oldest = %+v", got[1])
	}
	if limited, _ := Conversations(dir, 1); len(limited) != 1 || limited[0].ID != idTwo {
		t.Errorf("limit 1 = %+v", limited)
	}
}

func TestConversationsWithoutClaudeFolder(t *testing.T) {
	got, err := Conversations(filepath.Join(t.TempDir(), "none"), 10)
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestLargeTranscriptReadsItsEnds(t *testing.T) {
	dir := t.TempDir()
	filler := fmt.Sprintf(`{"type":"assistant","text":"%s"}`, strings.Repeat("x", historyHead+historyTail))
	writeTranscript(t, dir, "p", idOne, 0,
		`{"cwd":"/w/early"}`, filler, `{"type":"ai-title","aiTitle":"late title"}`)
	got, err := FindConversation(dir, idOne)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != "/w/early" || got.Title != "late title" {
		t.Errorf("got %+v", got)
	}
}

func TestFindConversation(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "p", idOne, 0, `{"cwd":"/w/one"}`)
	writeTranscript(t, dir, "q", "11111111-ffff-4fff-8fff-ffffffffffff", 0, `{"cwd":"/w/other"}`)
	writeTranscript(t, dir, "p", idTwo, 0, `{"cwd":"/w/two"}`)

	if c, err := FindConversation(dir, " "+idTwo+"\n"); err != nil || c.Cwd != "/w/two" {
		t.Errorf("full id: %+v, %v", c, err)
	}
	if c, err := FindConversation(dir, "22222222"); err != nil || c.ID != idTwo {
		t.Errorf("prefix: %+v, %v", c, err)
	}
	if _, err := FindConversation(dir, "11111111"); err == nil || !strings.Contains(err.Error(), "use more") {
		t.Errorf("ambiguous: %v", err)
	}
	if _, err := FindConversation(dir, "2222"); !errors.Is(err, ErrNoConversation) {
		t.Errorf("short: %v", err)
	}
	if _, err := FindConversation(dir, "99999999"); !errors.Is(err, ErrNoConversation) {
		t.Errorf("unknown: %v", err)
	}
}

func TestConfigDir(t *testing.T) {
	none := func(string) string { return "" }
	if got := ConfigDir(none, "/home/u"); got != "/home/u/.claude" {
		t.Errorf("default = %s", got)
	}
	set := func(string) string { return "/custom" }
	if got := ConfigDir(set, "/home/u"); got != "/custom" {
		t.Errorf("set = %s", got)
	}
}
