package core

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// agyHook is an agy payload with the common fields, plus extra ones.
func agyHook(t *testing.T, event, extra string) Event {
	t.Helper()
	raw := `{"conversationId":"conv-1","cwd":"/w","transcriptPath":"/w/.gemini/antigravity-cli/t.jsonl",` +
		`"hookEventName":"` + event + `","modelName":"auto","lastUserInput":"Fix the failing test"` + extra + `}`
	e, err := ParseHook(AntigravityHookPrefix+event, []byte(raw), at(1))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestParseAntigravityHook(t *testing.T) {
	anyEvent := func(Event) bool { return true }
	for _, tc := range []struct {
		event, extra string
		kind         EventKind
		check        func(Event) bool
	}{
		{"SessionStart", "", HookSessionStart, func(e Event) bool { return e.Source == "startup" && e.Prompt == "" }},
		{"PreInvocation", `,"invocationNum":1`, HookUserPromptSubmit, func(e Event) bool { return e.Prompt == "Fix the failing test" }},
		{"PostToolUse", `,"toolCall":{"id":"t1","name":"run_command","args":{"CommandLine":"go test"}},"error":"exit status 1"`, HookPostToolUse,
			func(e Event) bool { return e.ToolName == "run_command" && e.Prompt == "" }},
		{"Stop", `,"terminationReason":"NO_TOOL_CALL","fullyIdle":true`, HookStop, func(e Event) bool { return e.Error == "" }},
		{"Stop", `,"terminationReason":"USER_CANCELED"`, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"TERMINAL_STEP_TYPE"`, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"TERMINAL_CUSTOM_HOOK"`, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"INJECTED_RESPONSE"`, HookStop, anyEvent},
		{"Stop", ``, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"ERROR","error":"quota exceeded\u001b[0m"`, HookStopFailure, func(e Event) bool { return e.Error == "quota exceeded[0m" }},
		{"Stop", `,"terminationReason":"ERROR","error":""`, HookStopFailure, func(e Event) bool { return e.Error == "an error" }},
		{"Stop", `,"terminationReason":"MAX_INVOCATIONS"`, HookStopFailure, func(e Event) bool { return e.Error == "the model call limit reached" }},
		{"Stop", `,"terminationReason":"MAX_FORCED_INVOCATIONS"`, HookStopFailure, func(e Event) bool { return e.Error == "the continuation limit reached" }},
		{"Stop", `,"terminationReason":"MAX_TOKEN_BUDGET_EXCEEDED"`, HookStopFailure, func(e Event) bool { return e.Error == "the token budget used up" }},
		{"Stop", `,"terminationReason":"MAX_SOMETHING_NEW"`, HookStopFailure, func(e Event) bool { return e.Error == "a limit reached (MAX_SOMETHING_NEW)" }},
		{"PostInvocation", "", EventKind("antigravity:PostInvocation"), anyEvent},
	} {
		e := agyHook(t, tc.event, tc.extra)
		if e.Kind != tc.kind || e.SessionID != "conv-1" || e.Cwd != "/w" || e.Agent != "Antigravity" || !e.Time.Equal(at(1)) ||
			e.TranscriptPath != "" || !tc.check(e) {
			t.Errorf("%s %s: %+v", tc.event, tc.extra, e)
		}
	}
}

func TestParseAntigravityHookName(t *testing.T) {
	// With no event after the prefix, the payload's name is used.
	e, err := ParseHook(AntigravityHookPrefix, []byte(`{"conversationId":"c","hookEventName":"Stop"}`), time.Time{})
	if err != nil || e.Kind != HookStop {
		t.Errorf("payload name: %v %v", e.Kind, err)
	}
	if !IsAntigravityHook("antigravity:Stop") || IsAntigravityHook("Stop") {
		t.Error("IsAntigravityHook")
	}
}

func TestParseAntigravityHookErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid json":       `{`,
		"no conversation id": `{"hookEventName":"Stop","session_id":"claude-style"}`,
	} {
		if _, err := ParseHook("antigravity:Stop", []byte(raw), time.Time{}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// An agy session goes through the same states as a Claude one, without waiting.
func TestAntigravityLifecycle(t *testing.T) {
	s := &Session{ID: "s1", HarnessKind: HarnessAntigravity, Kind: KindHosted, NameSource: NameAuto}
	if !s.IsAntigravity() || s.IsTerminal() {
		t.Fatal("harness checks")
	}
	Apply(s, Event{Kind: EventCreated, Time: at(0)})
	steps := []struct {
		event, extra string
		want         Status
	}{
		{"SessionStart", "", StatusDone},
		{"PreInvocation", "", StatusBusy},
		{"PostToolUse", `,"toolCall":{"name":"view_file"}`, StatusBusy},
		{"Stop", `,"terminationReason":"NO_TOOL_CALL"`, StatusDone},
		{"PreInvocation", "", StatusBusy},
		{"Stop", `,"terminationReason":"ERROR","error":"boom"`, StatusError},
	}
	for i, st := range steps {
		e := agyHook(t, st.event, st.extra)
		e.Time = at(i + 1)
		if !s.FollowConversation(e) {
			t.Fatalf("%s: dropped", st.event)
		}
		Apply(s, e)
		if s.Status != st.want {
			t.Fatalf("%s: status %s, want %s", st.event, s.Status, st.want)
		}
	}
	if s.ClaudeSessionID != "conv-1" || s.Cwd != "/w" || s.TranscriptPath != "" || s.StatusDetail != "boom" {
		t.Errorf("session: %+v", s)
	}
	if s.Name != NameFromPrompt("Fix the failing test") || s.Name == "" {
		t.Errorf("name %q", s.Name)
	}
	it, _ := InboxEffect(s, agyHook(t, "Stop", `,"terminationReason":"ERROR","error":"boom"`))
	if it == nil || it.Kind != InboxError || it.Text != "Antigravity stopped with boom." {
		t.Errorf("inbox: %+v", it)
	}
	if it, drop := InboxEffect(s, agyHook(t, "Stop", `,"terminationReason":"NO_TOOL_CALL"`)); it != nil || !drop {
		t.Errorf("done turn: %+v %v", it, drop)
	}
	// Claude's text is unchanged.
	it, _ = InboxEffect(s, Event{Kind: HookStopFailure, Error: "rate_limit"})
	if it == nil || !strings.HasPrefix(it.Text, "Claude stopped with rate_limit") {
		t.Errorf("claude inbox: %+v", it)
	}
}

// agyPermissionScreen is the bottom of a real agy 1.5.10 screen at a
// permission prompt (from the host's ScreenText), with what came before it and
// the blank rows below.
const agyPermissionScreen = `> Run exactly this shell command and tell me what it prints: ls -la notes2.txt

  I'll run that for you.

Requesting permission for:
   ls -la notes2.txt
Run this command?
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with
'ls'
  3. Yes, and always allow for commands that start with 'ls' (Persist to
settings.json)
  4. No, cancel
  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel                                     plan · Gemini 3.8 Flash · high


`

func TestAntigravityOptions(t *testing.T) {
	want := []string{
		"Yes, run command",
		"Yes, and always allow in this conversation for commands that start with 'ls'",
		"Yes, and always allow for commands that start with 'ls' (Persist to settings.json)",
		"No, cancel",
	}
	if got := AntigravityOptions(agyPermissionScreen); !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := AntigravityOptions(strings.ReplaceAll(agyPermissionScreen, "\n", "\r\n")); !slices.Equal(got, want) {
		t.Errorf("CRLF: %q", got)
	}

	for name, screen := range map[string]string{
		// Unnumbered: arrows and Enter. No options; the item shows the message.
		"trust": "Do you trust the files in this folder?\n> Yes, I trust this folder\n  No, exit\n",
		// An earlier list, with agy's work since below it, isn't the prompt in front.
		"old list": agyPermissionScreen + "  ran ls\n  -rw-r--r-- notes2.txt\n  Done.\n\n> next prompt\n  more\n",
		"one":      "Pick:\n> 1. Only\n  ↑/↓ Navigate\n",
		"order":    "Pick:\n> 1. A\n  3. C\n",
		"nothing":  "",
		"no 1":     "  2. B\n  3. C\n",
	} {
		if got := AntigravityOptions(screen); got != nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	// A question: the list ends at a blank line, with the hints below.
	q := "Which database?\n\n  1. Postgres\n› 2. SQLite\n\nEnter to select · Esc to cancel\n"
	if got := AntigravityOptions(q); !slices.Equal(got, []string{"Postgres", "SQLite"}) {
		t.Errorf("question: %q", got)
	}
}

func TestAntigravityAsk(t *testing.T) {
	s := &Session{ID: "s1"}
	e := Event{Kind: HookNotification, Message: "Approve Bash: ls -la notes2.txt", Time: at(1)}
	it := AntigravityAsk(s, e, "permission", agyPermissionScreen)
	if it == nil || it.Kind != InboxPermission || it.Text != e.Message || len(it.Options) != 4 || it.SessionID != "s1" || !it.At.Equal(at(1)) {
		t.Fatalf("permission: %+v", it)
	}
	for kind, want := range map[string]string{"question": InboxQuestion, "plan": InboxQuestion} {
		if it := AntigravityAsk(s, e, kind, ""); it == nil || it.Kind != want || it.Options != nil {
			t.Errorf("%s: %+v", kind, it)
		}
	}
	if it := AntigravityAsk(s, e, "auth", agyPermissionScreen); it != nil {
		t.Errorf("sign in: %+v", it)
	}
	if it := AntigravityAsk(s, Event{Message: "x"}, "permission", ""); it == nil || it.At.IsZero() {
		t.Errorf("no time: %+v", it)
	}
}
