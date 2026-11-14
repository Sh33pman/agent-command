package core

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestParseHook(t *testing.T) {
	raw := []byte(`{"hook_event_name":"Notification","session_id":"abc","cwd":"/w",
		"transcript_path":"/t.jsonl","notification_type":"permission_prompt",
		"message":"Claude needs your permission","tool_input":{"command":"rm -rf /"}}`)
	e, err := ParseHook("", raw, at(1))
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != HookNotification || e.SessionID != "abc" || e.Cwd != "/w" || e.TranscriptPath != "/t.jsonl" ||
		e.NotificationType != "permission_prompt" || e.Message != "Claude needs your permission" || !e.Time.Equal(at(1)) {
		t.Errorf("unexpected event: %+v", e)
	}

	// The invoked name wins over the payload's.
	e, err = ParseHook("Stop", raw, at(1))
	if err != nil || e.Kind != HookStop {
		t.Errorf("name override: %v %v", e.Kind, err)
	}
}

func TestParseHookStopFailureError(t *testing.T) {
	for _, raw := range []string{
		`{"session_id":"a","error":"rate_limit"}`,
		`{"session_id":"a","error":{"type":"rate_limit","message":"slow down"}}`,
	} {
		e, err := ParseHook("StopFailure", []byte(raw), time.Time{})
		if err != nil || e.Error != "rate_limit" {
			t.Errorf("%s -> %q, %v", raw, e.Error, err)
		}
	}
	e, _ := ParseHook("StopFailure", []byte(`{"session_id":"a","error":42}`), time.Time{})
	if e.Error != "" {
		t.Errorf("non-string error should be empty, got %q", e.Error)
	}
}

func TestParseHookErrors(t *testing.T) {
	cases := map[string][]byte{
		"invalid json":  []byte(`{`),
		"no name":       []byte(`{"session_id":"a"}`),
		"no session id": []byte(`{"hook_event_name":"Stop"}`),
	}
	for name, raw := range cases {
		if _, err := ParseHook("", raw, time.Time{}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	big := make([]byte, MaxHookPayload+1)
	if _, err := ParseHook("Stop", big, time.Time{}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("oversized payload: %v", err)
	}
}

func TestParseHookSanitizes(t *testing.T) {
	raw := []byte(`{"session_id":"a","message":"hi\u001b]0;pwned\u0007 there\nnext","prompt":"` + strings.Repeat("x", 5000) + `"}`)
	e, err := ParseHook("Notification", raw, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if e.Message != "hi]0;pwned there next" {
		t.Errorf("message = %q", e.Message)
	}
	if len([]rune(e.Prompt)) != 4096 {
		t.Errorf("prompt not truncated: %d runes", len([]rune(e.Prompt)))
	}
}

func FuzzParseHook(f *testing.F) {
	f.Add("Stop", []byte(`{"session_id":"a","hook_event_name":"Stop"}`))
	f.Add("", []byte(`{"session_id":"a","hook_event_name":"Notification","notification_type":"idle_prompt","message":"\u001b[31m"}`))
	f.Add("StopFailure", []byte(`{"session_id":"a","error":{"type":"overloaded"}}`))
	f.Add("SessionStart", []byte(`{"session_id":"a","source":"fork","cwd":"/x"}`))
	f.Fuzz(func(t *testing.T, name string, raw []byte) {
		e, err := ParseHook(name, raw, time.Time{})
		if err != nil {
			return
		}
		for _, s := range []string{string(e.Kind), e.SessionID, e.Message, e.Prompt, e.Cwd, e.ToolName, e.Error} {
			for _, r := range s {
				if unicode.IsControl(r) && s != string(e.Kind) {
					t.Fatalf("control character %q survived in %q", r, s)
				}
			}
		}
		// Every parsed event must be safe to apply.
		var sess Session
		Apply(&sess, e)
	})
}

func TestParseHookBackgroundWork(t *testing.T) {
	at := time.Now()
	cases := []struct {
		name, payload string
		task          string
		wakeup        bool
		done          string
	}{
		{"PostToolUse", `{"session_id":"s","tool_response":{"backgroundTaskId":"bk0n6canm","stdout":""}}`, "bk0n6canm", false, ""},
		{"PostToolUse", `{"session_id":"s","tool_response":{"status":"async_launched","agentId":"a1b2"}}`, "a1b2", false, ""},
		{"PostToolUse", `{"session_id":"s","tool_response":{"status":"completed","agentId":"a1b2"}}`, "", false, ""},
		{"PostToolUse", `{"session_id":"s","tool_response":"plain text"}`, "", false, ""},
		{"PreToolUse", `{"session_id":"s","tool_response":{"backgroundTaskId":"x"}}`, "", false, ""},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"<task-notification>\n<task-id>b1\u001b</task-id>\n<status>failed</status>"}`, "", true, "b1"},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"  <task-notification> no id"}`, "", true, ""},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"<task-notification><task-id>open"}`, "", true, ""},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"what does <task-notification> mean?"}`, "", false, ""},
		{"Stop", `{"session_id":"s","prompt":"<task-notification><task-id>b1</task-id>"}`, "", false, ""},
	}
	for _, c := range cases {
		e, err := ParseHook(c.name, []byte(c.payload), at)
		if err != nil {
			t.Fatalf("%s %s: %v", c.name, c.payload, err)
		}
		if e.BackgroundTask != c.task || e.Wakeup != c.wakeup || e.TaskDone != c.done {
			t.Errorf("%s %s: task %q wakeup %v done %q", c.name, c.payload, e.BackgroundTask, e.Wakeup, e.TaskDone)
		}
	}
}

func TestWakeupDoesNotNameASession(t *testing.T) {
	s := &Session{ID: "s"}
	e, err := ParseHook("UserPromptSubmit", []byte(`{"session_id":"s","prompt":"<task-notification><task-id>b1</task-id>"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if Apply(s, e); s.Name != "" || s.Status != StatusBusy {
		t.Errorf("named %q, %s", s.Name, s.Status)
	}
}
