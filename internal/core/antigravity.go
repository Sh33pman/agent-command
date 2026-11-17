package core

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AntigravityHookPrefix starts the event name of a hook that Antigravity's CLI
// (agy) runs, as in `acmd hook antigravity:Stop`. Its payloads aren't Claude's,
// and agy reads the hook's answer from stdout.
const AntigravityHookPrefix = "antigravity:"

// IsAntigravityHook reports whether name, an `acmd hook` event, comes from agy.
func IsAntigravityHook(name string) bool { return strings.HasPrefix(name, AntigravityHookPrefix) }

// antigravityAgent is how messages name agy.
const antigravityAgent = "Antigravity"

// agyPayload is the part of an agy hook's stdin acmd reads (camelCase, from
// protojson).
type agyPayload struct {
	ConversationID string `json:"conversationId"`
	HookEventName  string `json:"hookEventName"`
	Cwd            string `json:"cwd"`
	LastUserInput  string `json:"lastUserInput"`
	ToolCall       struct {
		Name string `json:"name"`
	} `json:"toolCall"`
	TerminationReason string          `json:"terminationReason"`
	Error             json.RawMessage `json:"error"`
}

// agyLimits are the Stop reasons for a turn cut short by a limit, said as
// "Antigravity stopped with …".
var agyLimits = map[string]string{
	"MAX_INVOCATIONS":           "the model call limit reached",
	"MAX_FORCED_INVOCATIONS":    "the continuation limit reached",
	"MAX_TOKEN_BUDGET_EXCEEDED": "the token budget used up",
}

// parseAntigravityHook turns an agy hook payload into Claude's vocabulary, so
// the state machine, the inbox and notifications treat both alike. agy has no
// event for a permission prompt, so its sessions are never "waiting".
//
// transcriptPath is left out on purpose: the transcript tail looks for Claude's
// interrupt marker, which an agy transcript can contain as ordinary text. agy
// sends Stop when the user cancels, so it doesn't need the tail.
func parseAntigravityHook(event string, raw []byte, at time.Time) (Event, error) {
	var p agyPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Event{}, fmt.Errorf("parse antigravity hook payload: %w", err)
	}
	if event == "" {
		event = p.HookEventName
	}
	if p.ConversationID == "" {
		return Event{}, errors.New("antigravity hook payload: no conversationId")
	}
	e := Event{
		Time:      at,
		SessionID: clean(p.ConversationID, 128),
		Cwd:       clean(p.Cwd, 4096),
		Agent:     antigravityAgent,
	}
	switch event {
	case "SessionStart":
		e.Kind, e.Source = HookSessionStart, "startup"
	case "PreInvocation":
		// Before each model call of a turn: like a submitted prompt, it means
		// busy, and the user's prompt can name the session.
		e.Kind, e.Prompt = HookUserPromptSubmit, clean(p.LastUserInput, 4096)
	case "PostToolUse":
		e.Kind, e.ToolName = HookPostToolUse, clean(p.ToolCall.Name, 128)
	case "Stop":
		reason := p.TerminationReason
		switch {
		case reason == "ERROR":
			e.Kind, e.Error = HookStopFailure, cmp.Or(clean(errorString(p.Error), 128), "an error")
		case agyLimits[reason] != "":
			e.Kind, e.Error = HookStopFailure, agyLimits[reason]
		case strings.HasPrefix(reason, "MAX_"): // a limit added since
			e.Kind, e.Error = HookStopFailure, "a limit reached ("+clean(reason, 64)+")"
		default:
			// NO_TOOL_CALL, USER_CANCELED, TERMINAL_*, INJECTED_RESPONSE: the
			// turn is over, and the user has the prompt back.
			e.Kind = HookStop
		}
	default:
		// Not one acmd registers; the state machine ignores it.
		e.Kind = EventKind(AntigravityHookPrefix + clean(event, 64))
	}
	return e, nil
}

// AntigravityAsk is the Inbox item for agy waiting on you, from its blocked
// report (e, of kind permission, question or plan) and its screen at the
// time: the report's message, and the numbered answers the screen shows, which
// are answered by typing the number. Other kinds (a sign in) and a prompt with
// unnumbered answers (the folder trust prompt) are answered in the session, so
// they make no item, or one without options.
func AntigravityAsk(s *Session, e Event, kind, screen string) *InboxItem {
	var item string
	switch kind {
	case "permission":
		item = InboxPermission
	case "question", "plan":
		item = InboxQuestion
	default:
		return nil
	}
	at := e.Time
	if at.IsZero() {
		at = time.Now()
	}
	return &InboxItem{SessionID: s.ID, Kind: item, Text: e.Message, Options: AntigravityOptions(screen), At: at}
}

// agyOption is a line that starts an answer: "  2. Yes, and…", or "> 1. Yes"
// for the one selected.
var agyOption = regexp.MustCompile(`^\s*(?:[>❯›]\s*)?(\d{1,2})\.\s+(\S.*)$`)

const (
	// agyPromptLines is how far up from the bottom the answers can start.
	agyPromptLines = 40
	// agyFooterLines is how many lines of hints can follow them.
	agyFooterLines = 4
)

// AntigravityOptions reads the numbered answers of the prompt at the bottom of
// agy's screen, in order, without their numbers. agy wraps a long answer onto
// lines of its own without a number; they're joined back. The list ends at a
// blank line or agy's key hints ("↑/↓ Navigate · …", "esc to cancel"). nil
// when the bottom of the screen has no such list.
func AntigravityOptions(screen string) []string {
	lines := strings.Split(strings.ReplaceAll(screen, "\r", ""), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	first := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-agyPromptLines; i-- {
		if m := agyOption.FindStringSubmatch(lines[i]); m != nil && m[1] == "1" {
			first = i
			break
		}
	}
	if first < 0 {
		return nil
	}
	var opts []string
	i := first
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || agyHint(line) {
			break
		}
		if m := agyOption.FindStringSubmatch(lines[i]); m != nil {
			if m[1] != strconv.Itoa(len(opts)+1) {
				return nil // out of order: not a list of answers
			}
			opts = append(opts, strings.TrimSpace(m[2]))
			continue
		}
		opts[len(opts)-1] += " " + line
	}
	// Below the answers there are only hints: more, and the list is something
	// earlier on the screen, not the prompt in front.
	rest := 0
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			rest++
		}
	}
	if rest > agyFooterLines || len(opts) < 2 {
		return nil
	}
	return opts
}

// agyHint says whether a line is one of agy's key hints, which end a list.
func agyHint(line string) bool {
	lower := strings.ToLower(line)
	return strings.ContainsAny(line, "↑↓") || strings.Contains(line, " · ") ||
		strings.HasPrefix(lower, "esc ") || strings.HasPrefix(lower, "enter ") || strings.HasPrefix(lower, "tab ")
}
