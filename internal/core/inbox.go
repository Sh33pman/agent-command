package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Inbox items are what sessions have sent you: finished turns, questions,
// permission prompts and errors. The queue is the open ones. A session has at most
// one open, its latest, and it stops being open when the session moves on.

// Inbox item kinds.
const (
	InboxDone       = "done"       // a finished turn: Claude's final message
	InboxQuestion   = "question"   // AskUserQuestion: pick an option
	InboxPermission = "permission" // a tool waits for permission
	InboxError      = "error"      // the turn stopped with an API error
)

// Inbox item states. Only open items are in the queue.
const (
	InboxOpen      = "open"
	InboxResponded = "responded"
	InboxDismissed = "dismissed"
	InboxStale     = "stale" // no longer relevant: the session moved on
)

// InboxItem is one thing a session sent you.
type InboxItem struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Options   []string  `json:"options,omitempty"`   // question and permission answers
	Multipart bool      `json:"multipart,omitempty"` // several questions: answered in the session
	At        time.Time `json:"at"`
	Read      bool      `json:"read"`
	State     string    `json:"state"`
	HandledAt time.Time `json:"handled_at"`
}

// PermissionOptions are the answers to a permission prompt, in Claude's
// order (1, 2, then No).
var PermissionOptions = []string{"Yes", "Yes, and don't ask again", "No"}

// InboxEffect is what event e does to session s's inbox item: a new item
// (replacing any), drop (remove it), or neither.
func InboxEffect(s *Session, e Event) (item *InboxItem, drop bool) {
	at := e.Time
	if at.IsZero() {
		at = time.Now()
	}
	switch e.Kind {
	case HookStop:
		if e.LastMessage == "" {
			return nil, true
		}
		return &InboxItem{SessionID: s.ID, Kind: InboxDone, Text: e.LastMessage, At: at}, false
	case HookStopFailure:
		msg := firstNonEmpty(e.Error, e.Message, "an API error")
		return &InboxItem{SessionID: s.ID, Kind: InboxError, Text: "Claude stopped with " + msg + ".", At: at}, false
	case HookPermissionRequest:
		if e.ToolName == "AskUserQuestion" {
			if it := questionItem(s, e, at); it != nil {
				return it, false
			}
		}
		return &InboxItem{SessionID: s.ID, Kind: InboxPermission, Text: permissionText(e),
			Options: PermissionOptions, At: at}, false
	case HookUserPromptSubmit, HookPreToolUse, HookPostToolUse, HookPostToolUseFailure, HookSessionEnd,
		EventArchived, EventFinished, EventResumed:
		// The session moved on (the user answered or worked in it, or it
		// stopped): what was waiting is stale.
		return nil, true
	}
	return nil, false
}

// questionItem shows AskUserQuestion's questions. One with several parts has no
// options to pick, because it's answered in the session.
func questionItem(s *Session, e Event, at time.Time) *InboxItem {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
			Header   string `json:"header"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(e.ToolInput, &in) != nil || len(in.Questions) == 0 {
		return nil
	}
	q := in.Questions[0]
	var b strings.Builder
	b.WriteString(q.Question)
	var opts []string
	for i, o := range q.Options {
		opts = append(opts, o.Label)
		fmt.Fprintf(&b, "\n\n%d. %s", i+1, o.Label)
		if o.Description != "" {
			b.WriteString(": " + o.Description)
		}
	}
	for _, more := range in.Questions[1:] {
		b.WriteString("\n\nThen: " + more.Question)
	}
	multipart := len(in.Questions) > 1
	if multipart {
		opts = nil
	}
	return &InboxItem{SessionID: s.ID, Kind: InboxQuestion, Text: b.String(), Options: opts, Multipart: multipart, At: at}
}

// permissionText describes what a tool wants to do.
func permissionText(e Event) string {
	var in map[string]any
	_ = json.Unmarshal(e.ToolInput, &in)
	str := func(k string) string { v, _ := in[k].(string); return v }
	tool := firstNonEmpty(e.ToolName, "a tool")
	var b strings.Builder
	fmt.Fprintf(&b, "Claude wants to use %s", tool)
	switch {
	case str("command") != "":
		b.WriteString(":\n\n" + str("command"))
	case str("file_path") != "":
		b.WriteString(" on " + str("file_path"))
	case str("url") != "":
		b.WriteString(" on " + str("url"))
	case len(e.ToolInput) > 0:
		b.WriteString(":\n\n" + string(e.ToolInput))
	default:
		b.WriteString(".")
	}
	if d := str("description"); d != "" {
		b.WriteString("\n\n" + d)
	}
	return b.String()
}
