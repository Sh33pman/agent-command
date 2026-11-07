// Package report builds bug reports and feature requests. Its redaction also
// guards the daemon log, since a report posts the log where anyone can read it.
package report

import (
	"bytes"
	"io"
	"os"
	"os/user"
	"regexp"
	"strings"
	"sync"
)

// What's replaced, in order. Log lines hold counts, ids and error text, never
// conversations, but error text comes from other programs and could hold anything.
var patterns = []struct {
	re   *regexp.Regexp
	with string
}{
	// Private keys span lines.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "<private key>"},
	// A user and password in a URL.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`), "${1}<credentials>@"},
	// Known kinds of token.
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), "<token>"},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), "<token>"},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), "<token>"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`), "<token>"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}(?:\.[A-Za-z0-9_-]+)?`), "<token>"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} <token>"},
	// name=value and "name": "value" for anything that sounds secret.
	{regexp.MustCompile(`(?i)(["']?[a-z0-9_.-]*(?:token|secret|password|passwd|pwd|api[_-]?key|apikey|auth|credential|cookie|session[_-]?key)[a-z0-9_.-]*["']?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`), "${1}<hidden>"},
	// A prompt given on a command line.
	{regexp.MustCompile(`(--prompt[= ])("[^"]*"|'[^']*'|\S+)`), "${1}<hidden>"},
	{regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`), "<email>"},
	// Long runs of base64 or hex that nothing above recognized. Session ids are shorter.
	{regexp.MustCompile(`\b[A-Za-z0-9+/_-]{48,}={0,2}`), "<long value>"},
}

// Redactor removes what shouldn't leave this Mac from text.
type Redactor struct {
	home string
	name string
}

// NewRedactor knows this user's home folder and account name.
func NewRedactor() Redactor {
	var r Redactor
	r.home, _ = os.UserHomeDir()
	if u, err := user.Current(); err == nil {
		r.name = u.Username
	}
	return r
}

// Text returns s with secrets, the account name and the home folder taken out.
func (r Redactor) Text(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.with)
	}
	if r.home != "" && r.home != "/" {
		s = strings.ReplaceAll(s, r.home, "~")
	}
	// Short names would match inside ordinary words.
	if len(r.name) >= 3 {
		name := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(r.name) + `\b`)
		s = name.ReplaceAllString(s, "<user>")
	}
	return s
}

// Writer redacts each line on its way to w, so what's in the log is clean before a
// report is ever made. A line is written when it's complete, as log.Logger's are.
func (r Redactor) Writer(w io.Writer) io.Writer {
	return &lineWriter{to: w, redact: r.Text}
}

type lineWriter struct {
	mu     sync.Mutex
	to     io.Writer
	redact func(string) string
	rest   []byte
}

func (l *lineWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rest = append(l.rest, b...)
	for {
		i := bytes.IndexByte(l.rest, '\n')
		if i < 0 {
			return len(b), nil
		}
		if _, err := io.WriteString(l.to, l.redact(string(l.rest[:i+1]))); err != nil {
			return 0, err
		}
		l.rest = l.rest[i+1:]
	}
}
