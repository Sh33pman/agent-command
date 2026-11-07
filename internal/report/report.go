package report

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Repo is where reports go. The code's own repository is private, so people who
// use the app couldn't file anything there.
const Repo = "brettmostert/homebrew-tap"

const (
	// logLines and logBytes bound the log in a report. GitHub allows 65,536
	// characters in an issue's body.
	logLines = 200
	logBytes = 40 << 10
	// urlBody bounds the text in a prefilled page's address.
	urlBody = 6000
)

// Label is the issue label for a kind of report.
func Label(kind string) string {
	if kind == "feature" {
		return "enhancement"
	}
	return "bug"
}

// Details is the part of a report about this install, which the person sees before
// it's sent. A bug report has the log's end, cleaned; a request only the version.
func Details(kind, version, macOS, logPath string, r Redactor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Agent Command** %s on macOS %s (%s)\n", version, macOS, runtime.GOARCH)
	if kind == "feature" {
		return b.String()
	}
	log, err := logEnd(logPath)
	if err != nil {
		fmt.Fprintf(&b, "\nNo daemon log: %s\n", r.Text(err.Error()))
		return b.String()
	}
	fmt.Fprintf(&b, "\n<details><summary>Daemon log (the last %d lines)</summary>\n\n```\n%s\n```\n\n</details>\n",
		logLines, safeFence(r.Text(log)))
	return b.String()
}

// logEnd returns the last lines logged. Each day has its own file, so just after
// midnight they run on from yesterday's.
func logEnd(logPath string) (string, error) {
	dated, err := filepath.Glob(logPath + ".*")
	if err != nil {
		return "", err
	}
	sort.Strings(dated)
	dated = append(dated, logPath) // today's is the newest
	var lines []string
	for i := len(dated) - 1; i >= 0 && len(lines) < logLines; i-- {
		text, err := fileEnd(dated[i])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if text != "" {
			lines = append(strings.Split(text, "\n"), lines...)
		}
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("nothing at %s", logPath)
	}
	if len(lines) > logLines {
		lines = lines[len(lines)-logLines:]
	}
	text := strings.Join(lines, "\n")
	if len(text) > logBytes {
		text = text[len(text)-logBytes:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:] // the first is only part of a line
		}
	}
	return text, nil
}

// fileEnd reads the end of a file, which logBytes makes enough for any report.
func fileEnd(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- acmd's own logs
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	from := max(info.Size()-logBytes, 0)
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	text := strings.ToValidUTF8(string(b), "")
	if from > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:] // the first is only part of a line
		}
	}
	return strings.TrimRight(text, "\n"), nil
}

// safeFence stops log text from ending the code block it's shown in.
func safeFence(s string) string {
	return strings.ReplaceAll(s, "```", "'''")
}

// IssueURL is GitHub's page for a new issue with the fields filled in, which the person
// submits themselves. An address can only hold so much, so a long body is cut at the log,
// and fits reports whether it all went in.
func IssueURL(kind, title, body string) (address string, fits bool) {
	fits = len(body) <= urlBody
	if !fits {
		if i := strings.Index(body, "<details>"); i >= 0 {
			body = body[:i]
		}
		if len(body) > urlBody {
			body = body[:urlBody]
		}
		body = strings.ToValidUTF8(body, "") + "\n\n_The full report, with the log, was too long for this page. Paste it here from the clipboard._\n"
	}
	q := url.Values{"title": {title}, "body": {body}, "labels": {Label(kind)}}
	return "https://github.com/" + Repo + "/issues/new?" + q.Encode(), fits
}
