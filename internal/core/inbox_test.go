package core

import (
	"strings"
	"testing"
	"time"
)

func TestInboxEffect(t *testing.T) {
	s := &Session{ID: "s1"}
	at := time.Unix(100, 0)
	hook := func(name, payload string) Event {
		t.Helper()
		e, err := ParseHook(name, []byte(payload), at)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	// A finished turn: Claude's final message, lines kept.
	it, drop := InboxEffect(s, hook("Stop", `{"session_id":"c","last_assistant_message":"Done.\n\n- tests pass\u001b[31m"}`))
	if drop || it == nil || it.Kind != InboxDone || it.Text != "Done.\n\n- tests pass[31m" || it.SessionID != "s1" || !it.At.Equal(at) {
		t.Errorf("stop: %+v %v", it, drop)
	}
	// A Stop without a message drops the old item.
	if it, drop := InboxEffect(s, hook("Stop", `{"session_id":"c"}`)); it != nil || !drop {
		t.Errorf("empty stop: %+v %v", it, drop)
	}

	// AskUserQuestion (from the recorded fixture): the first question's options.
	q := `{"session_id":"c","tool_name":"AskUserQuestion","tool_input":{"questions":[{"header":"Color","multiSelect":false,` +
		`"options":[{"description":"Pick red","label":"Red"},{"description":"Pick blue","label":"Blue"}],"question":"Do you prefer red or blue?"},` +
		`{"question":"And shape?","options":[{"label":"Round"}]}]}}`
	it, _ = InboxEffect(s, hook("PermissionRequest", q))
	if it.Kind != InboxQuestion || len(it.Options) != 0 || !it.Multipart ||
		!strings.HasPrefix(it.Text, "Do you prefer red or blue?\n\n1. Red: Pick red\n\n2. Blue: Pick blue") ||
		!strings.Contains(it.Text, "Then: And shape?") {
		t.Errorf("question: %+v", it)
	}

	// A tool permission: what it wants to run, and the three answers.
	it, _ = InboxEffect(s, hook("PermissionRequest", `{"session_id":"c","tool_name":"Bash","tool_input":{"command":"npm test","description":"Run the tests"}}`))
	if it.Kind != InboxPermission || it.Text != "Claude wants to use Bash:\n\nnpm test\n\nRun the tests" || len(it.Options) != 3 {
		t.Errorf("permission: %+v", it)
	}
	it, _ = InboxEffect(s, hook("PermissionRequest", `{"session_id":"c","tool_name":"Edit","tool_input":{"file_path":"/w/a.go"}}`))
	if it.Text != "Claude wants to use Edit on /w/a.go" {
		t.Errorf("edit permission: %q", it.Text)
	}
	it, _ = InboxEffect(s, hook("StopFailure", `{"session_id":"c","error":"rate_limit"}`))
	if it.Kind != InboxError || it.Text != "Claude stopped with rate_limit." {
		t.Errorf("failure: %+v", it)
	}

	// Moving on drops it; other events leave it.
	for _, name := range []string{"UserPromptSubmit", "PreToolUse", "SessionEnd"} {
		if _, drop := InboxEffect(s, hook(name, `{"session_id":"c","prompt":"x","tool_name":"Bash"}`)); !drop {
			t.Errorf("%s doesn't drop it", name)
		}
	}
	for _, k := range []EventKind{EventArchived, EventFinished} {
		if _, drop := InboxEffect(s, Event{Kind: k}); !drop {
			t.Errorf("%s doesn't drop it", k)
		}
	}
	if it, drop := InboxEffect(s, hook("Notification", `{"session_id":"c","notification_type":"idle_prompt"}`)); it != nil || drop {
		t.Errorf("idle notification: %+v %v", it, drop)
	}
	// Junk tool input is ignored.
	if e := hook("PermissionRequest", `{"session_id":"c","tool_name":"X","tool_input":"`+strings.Repeat("a", 20000)+`"}`); e.ToolInput != nil {
		t.Error("oversized tool input kept")
	}
}
