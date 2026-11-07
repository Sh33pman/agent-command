package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testRedactor = Redactor{home: "/Users/sam", name: "sam"}

func TestRedactRemovesSecrets(t *testing.T) {
	for _, secret := range []string{
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat_11ABCDEFG0abcdefghijkl_mnopqrstuv",
		"xoxb-123456789012-abcdefghij",
		"AKIAIOSFODNN7EXAMPLE",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.abcdefghijkl",
		"sam.jones@example.com",
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
		"-----BEGIN RSA PRIVATE KEY-----\nabc\ndef\n-----END RSA PRIVATE KEY-----",
	} {
		if got := testRedactor.Text("failed with " + secret + " here"); strings.Contains(got, secret) {
			t.Errorf("kept %q: %q", secret, got)
		}
	}
	for in, gone := range map[string]string{
		"Authorization: Bearer abcdef123456":      "abcdef123456",
		"password=hunter2 next":                   "hunter2",
		`"api_key": "abc def"`:                    "abc def",
		"MY_SECRET_TOKEN='s3 cret'":               "s3 cret",
		"https://me:hunter2@example.com/repo.git": "hunter2",
		"--prompt 'fix my private thing'":         "private thing",
		"--prompt=private":                        "=private",
	} {
		if got := testRedactor.Text(in); strings.Contains(got, gone) {
			t.Errorf("%q kept %q: %q", in, gone, got)
		}
	}
}

func TestRedactHidesWhoYouAre(t *testing.T) {
	got := testRedactor.Text("open /Users/sam/work/app: no such file, for user sam, SAM too, not sample")
	if strings.Contains(got, "/Users/sam") || !strings.Contains(got, "~/work/app") {
		t.Errorf("home: %q", got)
	}
	if !strings.Contains(got, "user <user>") || !strings.Contains(got, "<user> too") {
		t.Errorf("name: %q", got)
	}
	if !strings.Contains(got, "sample") {
		t.Errorf("changed an ordinary word: %q", got)
	}
}

func TestRedactKeepsWhatDiagnosesProblems(t *testing.T) {
	for _, line := range []string{
		"acmd daemon: 2026/11/07 11:26:23 listening on /tmp/x/daemon.sock (version 0.41.1, pid 123)",
		"reconcile: 0 discovered, 1 updated",
		"purge: claude rm 01M6TM4THGFS9GJFW8EC2K6WJE: exit status 1",
		"goroutine 1 [running]:",
		"main.go:42 +0x1c",
	} {
		if got := testRedactor.Text(line); got != line {
			t.Errorf("changed %q: %q", line, got)
		}
	}
}

func TestWriterRedactsWholeLines(t *testing.T) {
	var out bytes.Buffer
	w := testRedactor.Writer(&out)
	for _, part := range []string{"open /Users/s", "am/x\nnext pass", "word=abc\nunfinished"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if got := out.String(); got != "open ~/x\nnext password=<hidden>\n" {
		t.Errorf("wrote %q", got)
	}
}

func TestDetails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	var log strings.Builder
	for range logLines + 50 {
		log.WriteString("line\n")
	}
	log.WriteString("the last with sk-ant-api03-abcdefghijklmnopqrstuvwxyz and ```\n")
	if err := os.WriteFile(path, []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	bug := Details("bug", "1.2.3", "27.2", path, testRedactor)
	if !strings.Contains(bug, "1.2.3") || !strings.Contains(bug, "27.2") || !strings.Contains(bug, "the last with") {
		t.Errorf("bug details: %s", bug[:80])
	}
	if strings.Contains(bug, "sk-ant") {
		t.Error("the log's secret reached the report")
	}
	if strings.Count(bug, "```") != 2 {
		t.Errorf("the log can break out of its block: %d fences", strings.Count(bug, "```"))
	}
	if got := strings.Count(bug, "line\n"); got != logLines-1 {
		t.Errorf("%d plain lines, want %d", got, logLines-1)
	}
	if feature := Details("feature", "1.2.3", "27.2", path, testRedactor); strings.Contains(feature, "line") {
		t.Errorf("a request shouldn't include the log: %s", feature)
	}
	if none := Details("bug", "1.2.3", "27.2", filepath.Join(dir, "none"), testRedactor); !strings.Contains(none, "No daemon log") {
		t.Errorf("missing log: %s", none)
	}
}

func TestLogEndRunsOnFromYesterday(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	for name, text := range map[string]string{
		path:                 "today\n",
		path + ".2026-11-06": "yesterday\n",
		path + ".2026-11-05": strings.Repeat("older\n", logLines),
	} {
		if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := logEnd(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != logLines || lines[0] != "older" || lines[logLines-2] != "yesterday" || lines[logLines-1] != "today" {
		t.Errorf("%d lines, ending %q", len(lines), lines[len(lines)-2:])
	}
}

func TestLogEndBoundsVeryLongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte(strings.Repeat(strings.Repeat("x", 500)+"\n", logLines)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := logEnd(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > logBytes {
		t.Errorf("%d bytes", len(got))
	}
}

func TestIssueURLCutsLongBodiesAtTheLog(t *testing.T) {
	short, fits := IssueURL("feature", "A title", "words")
	if !fits || !strings.Contains(short, "labels=enhancement") || !strings.Contains(short, "title=A+title") {
		t.Errorf("short: %v %s", fits, short)
	}
	long, fits := IssueURL("bug", "T", "what happened\n<details>"+strings.Repeat("log", urlBody))
	if fits || !strings.Contains(long, "what+happened") || strings.Contains(long, "loglog") || !strings.Contains(long, "clipboard") {
		t.Errorf("long: %v, %d bytes", fits, len(long))
	}
}
