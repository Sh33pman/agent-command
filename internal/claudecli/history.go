package claudecli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A transcript is usually large and its details sit near the ends, so only
// these parts of a file are read.
const (
	historyHead = 64 << 10
	historyTail = 256 << 10
)

// minIDPrefix is the shortest id prefix accepted: Claude shows 8 characters.
const minIDPrefix = 8

// ErrNoConversation means no conversation has that id.
var ErrNoConversation = errors.New("no Claude conversation has that id")

// Conversation is an earlier Claude session, read from its transcript. The
// transcript format is Claude's own, so anything unexpected is skipped.
type Conversation struct {
	ID        string
	Cwd       string
	Title     string
	Branch    string
	UpdatedAt time.Time
}

// ConfigDir is Claude's folder: CLAUDE_CONFIG_DIR, or ~/.claude.
func ConfigDir(getenv func(string) string, home string) string {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".claude")
}

// Conversations lists up to limit conversations under Claude's folder, the most
// recently active first.
func Conversations(configDir string, limit int) ([]Conversation, error) {
	files, err := transcripts(configDir)
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, f := range files {
		if len(out) == limit {
			break
		}
		if c, ok := readConversation(f.path, f.modTime); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// FindConversation finds a conversation by its id or an id prefix.
func FindConversation(configDir, id string) (Conversation, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) < minIDPrefix {
		return Conversation{}, fmt.Errorf("%w: use at least %d characters of it", ErrNoConversation, minIDPrefix)
	}
	files, err := transcripts(configDir)
	if err != nil {
		return Conversation{}, err
	}
	var found []Conversation
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f.path), ".jsonl")
		if !strings.HasPrefix(name, id) {
			continue
		}
		if c, ok := readConversation(f.path, f.modTime); ok {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return Conversation{}, ErrNoConversation
	case 1:
		return found[0], nil
	}
	return Conversation{}, fmt.Errorf("%d Claude conversations start with %s: use more of the id", len(found), id)
}

type transcript struct {
	path    string
	modTime time.Time
}

// transcripts finds the transcript files, newest first. A session's file is
// named after its id; others, such as orphaned copies, are skipped.
func transcripts(configDir string) ([]transcript, error) {
	paths, err := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []transcript
	for _, p := range paths {
		if !isUUID(strings.TrimSuffix(filepath.Base(p), ".jsonl")) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		out = append(out, transcript{p, st.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdef", r) {
				return false
			}
		}
	}
	return true
}

// readConversation reads a transcript. One without a folder has nothing to
// resume, so it doesn't count as a conversation.
func readConversation(path string, modTime time.Time) (Conversation, bool) {
	data, err := readEnds(path)
	if err != nil {
		return Conversation{}, false
	}
	c := Conversation{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), UpdatedAt: modTime}
	var custom, ai, prompt string
	for _, line := range bytes.Split(data, []byte("\n")) {
		var entry struct {
			Cwd         string `json:"cwd"`
			GitBranch   string `json:"gitBranch"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
			LastPrompt  string `json:"lastPrompt"`
			IsSidechain bool   `json:"isSidechain"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.IsSidechain {
			continue // includes lines cut by reading only the ends
		}
		if entry.Cwd != "" {
			c.Cwd = entry.Cwd
		}
		if entry.GitBranch != "" {
			c.Branch = entry.GitBranch
		}
		if entry.CustomTitle != "" {
			custom = entry.CustomTitle
		}
		if entry.AITitle != "" {
			ai = entry.AITitle
		}
		if entry.LastPrompt != "" {
			prompt = entry.LastPrompt
		}
	}
	if c.Cwd == "" {
		return Conversation{}, false
	}
	for _, title := range []string{custom, ai, prompt} {
		if title != "" {
			c.Title = title
			break
		}
	}
	return c, true
}

func readEnds(path string) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() <= historyHead+historyTail {
		return os.ReadFile(filepath.Clean(path))
	}
	head := make([]byte, historyHead)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, err
	}
	tail := make([]byte, historyTail)
	if _, err := f.ReadAt(tail, st.Size()-historyTail); err != nil {
		return nil, err
	}
	return append(append(head, '\n'), tail...), nil
}
