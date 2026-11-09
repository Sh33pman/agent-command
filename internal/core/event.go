package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// EventKind identifies an input to the state machine: a Claude Code hook
// event name, or one of the synthetic kinds produced by acmd itself.
type EventKind string

// Hook events acmd registers (SPEC §9.1).
const (
	HookSessionStart       EventKind = "SessionStart"
	HookUserPromptSubmit   EventKind = "UserPromptSubmit"
	HookPreToolUse         EventKind = "PreToolUse"
	HookPostToolUse        EventKind = "PostToolUse"
	HookPostToolUseFailure EventKind = "PostToolUseFailure"
	HookPermissionRequest  EventKind = "PermissionRequest"
	HookNotification       EventKind = "Notification"
	HookStop               EventKind = "Stop"
	HookStopFailure        EventKind = "StopFailure"
	HookSubagentStart      EventKind = "SubagentStart"
	HookSubagentStop       EventKind = "SubagentStop"
	HookPreCompact         EventKind = "PreCompact"
	HookPostCompact        EventKind = "PostCompact"
	HookCwdChanged         EventKind = "CwdChanged"
	HookSessionEnd         EventKind = "SessionEnd"
)

// Synthetic events produced by acmd (SPEC §5.3, §5.4).
const (
	// EventCreated: acmd started `claude --bg` for this session.
	EventCreated EventKind = "acmd:created"
	// EventInterrupted: the transcript shows "[Request interrupted by user".
	EventInterrupted EventKind = "acmd:interrupted"
	// EventAgentStatus: `claude agents --json` reported Event.AgentStatus.
	EventAgentStatus EventKind = "acmd:agent-status"
	// EventArchived / EventUnarchived / EventResumed: explicit user actions.
	EventArchived   EventKind = "acmd:archived"
	EventUnarchived EventKind = "acmd:unarchived"
	EventResumed    EventKind = "acmd:resumed"
	// EventFinished: the user finished the session (acmd stopped it).
	EventFinished EventKind = "acmd:finished"
	// EventForeground: a terminal's host said what runs in front (Event.Front).
	EventForeground EventKind = "acmd:foreground"
)

// FrontShell is Event.Front when the terminal's own shell is in front.
const FrontShell = "$shell"

// agents are the programs a terminal row shows as an agent, by name.
var agents = map[string]bool{
	"claude": true, "opencode": true, "codex": true, "gemini": true, "aider": true,
	"amp": true, "cursor-agent": true, "goose": true, "crush": true, "qwen": true,
}

// IsAgent reports whether program, a name from a terminal's host, is a coding agent.
func IsAgent(program string) bool { return agents[program] }

// AgentStatus values from `claude agents --json` "status" (SPEC §5.3).
// AgentStopped stands for a listed session with no pid and no status (M0 C3).
const (
	AgentBusy    = "busy"
	AgentIdle    = "idle"
	AgentWaiting = "waiting"
	AgentStopped = "stopped"
)

// Event is one input to the state machine.
type Event struct {
	Kind             EventKind
	Time             time.Time
	SessionID        string // Claude session id
	Cwd              string
	TranscriptPath   string
	Source           string // SessionStart: startup|resume|clear|compact|fork
	Reason           string // SessionEnd reason
	NotificationType string // Notification matcher, e.g. permission_prompt
	ToolName         string
	Message          string
	Prompt           string // UserPromptSubmit only; used for the auto-name, never stored
	Error            string // StopFailure matcher
	AgentStatus      string // EventAgentStatus only
	Front            string // EventForeground only: FrontShell or a program's name
	// LastMessage is Claude's final message of the turn (Stop), lines kept.
	LastMessage string
	// ToolInput is the tool's input (PermissionRequest, PreToolUse), as
	// JSON, capped.
	ToolInput json.RawMessage
}

// MaxHookPayload caps hook stdin (SPEC §11.1).
const MaxHookPayload = 1 << 20

// ErrPayloadTooLarge is returned for hook payloads over MaxHookPayload.
var ErrPayloadTooLarge = errors.New("hook payload too large")

type hookPayload struct {
	HookEventName    string          `json:"hook_event_name"`
	SessionID        string          `json:"session_id"`
	Cwd              string          `json:"cwd"`
	TranscriptPath   string          `json:"transcript_path"`
	Source           string          `json:"source"`
	Reason           string          `json:"reason"`
	NotificationType string          `json:"notification_type"`
	ToolName         string          `json:"tool_name"`
	Message          string          `json:"message"`
	Prompt           string          `json:"prompt"`
	Error            json.RawMessage `json:"error"`
	LastMessage      string          `json:"last_assistant_message"`
	ToolInput        json.RawMessage `json:"tool_input"`
}

// Caps for the reply popup's data (SPEC §8.8).
const (
	maxLastMessage = 64 << 10
	maxToolInput   = 16 << 10
)

// ParseHook turns a hook's stdin payload into an Event. name is the event
// name acmd was invoked with (`acmd hook <Event>`); it wins over the
// payload's hook_event_name when both are set. Hook stdin is untrusted
// input, so strings are sanitized and size is capped.
func ParseHook(name string, raw []byte, at time.Time) (Event, error) {
	if len(raw) > MaxHookPayload {
		return Event{}, ErrPayloadTooLarge
	}
	var p hookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Event{}, fmt.Errorf("parse hook payload: %w", err)
	}
	kind := EventKind(name)
	if kind == "" {
		kind = EventKind(p.HookEventName)
	}
	if kind == "" {
		return Event{}, errors.New("hook payload: no event name")
	}
	if p.SessionID == "" {
		return Event{}, errors.New("hook payload: no session_id")
	}
	return Event{
		Kind:             kind,
		Time:             at,
		SessionID:        clean(p.SessionID, 128),
		Cwd:              clean(p.Cwd, 4096),
		TranscriptPath:   clean(p.TranscriptPath, 4096),
		Source:           clean(p.Source, 64),
		Reason:           clean(p.Reason, 64),
		NotificationType: clean(p.NotificationType, 64),
		ToolName:         clean(p.ToolName, 128),
		Message:          clean(p.Message, 512),
		Prompt:           clean(p.Prompt, 4096),
		Error:            clean(errorString(p.Error), 128),
		LastMessage:      cleanText(p.LastMessage, maxLastMessage),
		ToolInput:        capJSON(p.ToolInput, maxToolInput),
	}, nil
}

// cleanText is clean for multi-line text: line breaks and tabs stay.
func cleanText(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == limit {
			break
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// capJSON keeps raw only when it's valid JSON within limit bytes.
func capJSON(raw json.RawMessage, limit int) json.RawMessage {
	if len(raw) == 0 || len(raw) > limit || !json.Valid(raw) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// errorString accepts StopFailure's "error" as either a string or an object
// with a "type" field.
func errorString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Type
	}
	return ""
}

// clean strips control characters (including escape sequences' ESC) and
// truncates to limit runes, so values are safe for titles, notifications
// and logs (SPEC §11.1).
func clean(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == limit {
			break
		}
		if r == '\n' || r == '\t' {
			r = ' '
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
