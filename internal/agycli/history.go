// Package agycli reads what Antigravity's CLI (agy) keeps about earlier
// conversations, so they can be resumed with `agy --conversation <id>`.
package agycli

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // agy's conversation titles
)

// minIDPrefix is the shortest id prefix accepted, as for Claude's.
const minIDPrefix = 8

// maxLine bounds one line of history.jsonl: a line is a prompt.
const maxLine = 1 << 20

// ErrNoConversation means no conversation has that id.
var ErrNoConversation = errors.New("no Antigravity conversation has that id")

// Conversation is an earlier agy conversation, from agy's prompt history.
type Conversation struct {
	ID        string
	Cwd       string
	Title     string
	UpdatedAt time.Time
}

// Dir is agy's folder, ~/.gemini/antigravity-cli.
func Dir(home string) string { return filepath.Join(home, ".gemini", "antigravity-cli") }

// Conversations lists up to limit conversations from agy's history, the most
// recently active first. No history is no conversations.
func Conversations(dir string, limit int) ([]Conversation, error) {
	all, err := read(dir)
	if err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// FindConversation finds a conversation by its id or an id prefix.
func FindConversation(dir, id string) (Conversation, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) < minIDPrefix {
		return Conversation{}, fmt.Errorf("%w: use at least %d characters of it", ErrNoConversation, minIDPrefix)
	}
	all, err := read(dir)
	if err != nil {
		return Conversation{}, err
	}
	var found []Conversation
	for _, c := range all {
		if strings.HasPrefix(c.ID, id) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return Conversation{}, ErrNoConversation
	case 1:
		return found[0], nil
	}
	return Conversation{}, fmt.Errorf("%d Antigravity conversations start with %s: use more of the id", len(found), id)
}

// historyLine is one prompt in history.jsonl.
type historyLine struct {
	Display        string `json:"display"`
	Timestamp      int64  `json:"timestamp"` // Unix ms
	Workspace      string `json:"workspace"`
	ConversationID string `json:"conversationId"`
	Type           string `json:"type"`
}

// read groups history.jsonl's prompts by conversation: its folder is the
// latest prompt's, its time the latest prompt's, and its title the first
// prompt that isn't a slash command (or agy's own title for it). A line
// without a conversation, or one acmd can't read, is skipped. Newest first.
func read(dir string) ([]Conversation, error) {
	f, err := os.Open(filepath.Join(filepath.Clean(dir), "history.jsonl")) // #nosec G304 -- agy's own file
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	byID := map[string]*Conversation{}
	r := bufio.NewReader(f)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			add(byID, line)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	titles := summaryTitles(filepath.Join(dir, "conversation_summaries.db"))
	out := make([]Conversation, 0, len(byID))
	for _, c := range byID {
		if c.Cwd == "" {
			continue // nothing to resume it in
		}
		if t := titles[c.ID]; t != "" {
			c.Title = t
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// readLine reads one line, without its newline; one over maxLine is dropped.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if len(line)+len(part) <= maxLine {
			line = append(line, part...)
		} else {
			line = nil
		}
		if err != nil || !isPrefix {
			return line, err
		}
	}
}

func add(byID map[string]*Conversation, line []byte) {
	var h historyLine
	if json.Unmarshal(line, &h) != nil || !isID(h.ConversationID) {
		return
	}
	c := byID[h.ConversationID]
	if c == nil {
		c = &Conversation{ID: h.ConversationID}
		byID[h.ConversationID] = c
	}
	at := time.UnixMilli(h.Timestamp).UTC()
	if !at.Before(c.UpdatedAt) {
		c.UpdatedAt = at
		if h.Workspace != "" {
			c.Cwd = h.Workspace
		}
	} else if c.Cwd == "" {
		c.Cwd = h.Workspace
	}
	prompt := strings.TrimSpace(h.Display)
	if c.Title == "" && h.Type != "slash_command" && prompt != "" && !strings.HasPrefix(prompt, "/") {
		c.Title = firstLine(prompt)
	}
}

// isID accepts agy's conversation ids (UUIDs), and nothing a flag or a path
// could be made of.
func isID(s string) bool {
	if len(s) < minIDPrefix || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef-", r) {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// summaryTitles reads the titles agy gives conversations, by id. The file is
// agy's: it is opened read-only and immutable, so acmd takes no lock agy could
// trip on, and anything wrong with it means no titles.
func summaryTitles(path string) map[string]string {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT conversation_id, title FROM conversation_summaries WHERE title != ''`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	titles := map[string]string{}
	for rows.Next() {
		var id, title string
		if rows.Scan(&id, &title) == nil {
			titles[id] = firstLine(title)
		}
	}
	return titles
}
