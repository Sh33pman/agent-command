package core

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func TestApplyTransitions(t *testing.T) {
	tests := []struct {
		name       string
		from       Status
		event      Event
		want       Status
		wantDetail string
		applied    bool
	}{
		{"created", "", Event{Kind: EventCreated}, StatusStarting, "", true},
		{"startup is ready", StatusStarting, Event{Kind: HookSessionStart, Source: "startup"}, StatusDone, "", true},
		{"resume is ready", StatusPaused, Event{Kind: HookSessionStart, Source: "resume"}, StatusDone, "", true},
		{"compact keeps status", StatusBusy, Event{Kind: HookSessionStart, Source: "compact"}, StatusBusy, "", true},
		{"prompt -> busy", StatusDone, Event{Kind: HookUserPromptSubmit}, StatusBusy, "", true},
		{"prompt clears error", StatusError, Event{Kind: HookUserPromptSubmit}, StatusBusy, "", true},
		{"tool -> busy with detail", StatusDone, Event{Kind: HookPreToolUse, ToolName: "Bash"}, StatusBusy, "Bash", true},
		{"answered question -> busy", StatusWaiting, Event{Kind: HookPostToolUse, ToolName: "AskUserQuestion"}, StatusBusy, "", true},
		{"subagent start -> busy", StatusDone, Event{Kind: HookSubagentStart}, StatusBusy, "", true},
		{"subagent stop after Stop keeps done", StatusDone, Event{Kind: HookSubagentStop}, StatusDone, "", true},
		{"post compact no change", StatusDone, Event{Kind: HookPostCompact}, StatusDone, "", true},
		{"question -> waiting", StatusBusy, Event{Kind: HookPermissionRequest, ToolName: "AskUserQuestion"}, StatusWaiting, "question", true},
		{"permission -> waiting", StatusBusy, Event{Kind: HookPermissionRequest, ToolName: "Bash"}, StatusWaiting, "permission: Bash", true},
		{"permission_prompt -> waiting", StatusBusy, Event{Kind: HookNotification, NotificationType: "permission_prompt", Message: "Claude needs your permission"}, StatusWaiting, "Claude needs your permission", true},
		{"agent_needs_input -> waiting", StatusBusy, Event{Kind: HookNotification, NotificationType: "agent_needs_input"}, StatusWaiting, "agent_needs_input", true},
		{"idle_prompt never overrides waiting", StatusWaiting, Event{Kind: HookNotification, NotificationType: "idle_prompt"}, StatusWaiting, "", true},
		{"idle_prompt recovers busy", StatusBusy, Event{Kind: HookNotification, NotificationType: "idle_prompt"}, StatusDone, "", true},
		{"idle_prompt on done no change", StatusDone, Event{Kind: HookNotification, NotificationType: "idle_prompt"}, StatusDone, "", true},
		{"unknown notification ignored", StatusBusy, Event{Kind: HookNotification, NotificationType: "auth_success"}, StatusBusy, "", true},
		{"stop -> done", StatusBusy, Event{Kind: HookStop}, StatusDone, "", true},
		{"stop failure -> error", StatusBusy, Event{Kind: HookStopFailure, Error: "rate_limit"}, StatusError, "rate_limit", true},
		{"stop failure fallback detail", StatusBusy, Event{Kind: HookStopFailure}, StatusError, "API error", true},
		{"esc interrupt -> done", StatusBusy, Event{Kind: EventInterrupted}, StatusDone, "", true},
		{"denied permission -> done", StatusWaiting, Event{Kind: EventInterrupted}, StatusDone, "", true},
		{"interrupt when done no change", StatusDone, Event{Kind: EventInterrupted}, StatusDone, "", true},
		{"interrupt doesn't hide an error", StatusError, Event{Kind: EventInterrupted}, StatusError, "", true},
		{"agents busy", StatusDone, Event{Kind: EventAgentStatus, AgentStatus: AgentBusy}, StatusBusy, "", true},
		{"agents waiting", StatusBusy, Event{Kind: EventAgentStatus, AgentStatus: AgentWaiting}, StatusWaiting, "", true},
		{"agents idle -> done", StatusBusy, Event{Kind: EventAgentStatus, AgentStatus: AgentIdle}, StatusDone, "", true},
		{"agents idle keeps waiting", StatusWaiting, Event{Kind: EventAgentStatus, AgentStatus: AgentIdle}, StatusWaiting, "", true},
		{"agents idle keeps error", StatusError, Event{Kind: EventAgentStatus, AgentStatus: AgentIdle}, StatusError, "", true},
		{"agents stopped -> paused", StatusDone, Event{Kind: EventAgentStatus, AgentStatus: AgentStopped}, StatusPaused, "stopped", true},
		{"session end other -> paused", StatusDone, Event{Kind: HookSessionEnd, Reason: "other"}, StatusPaused, "other", true},
		{"session end exit -> paused", StatusDone, Event{Kind: HookSessionEnd, Reason: "prompt_input_exit"}, StatusPaused, "prompt_input_exit", true},
		{"session end clear ignored", StatusDone, Event{Kind: HookSessionEnd, Reason: "clear"}, StatusDone, "", true},
		{"archive", StatusDone, Event{Kind: EventArchived}, StatusArchived, "", true},
		{"resume paused -> starting", StatusPaused, Event{Kind: EventResumed}, StatusStarting, "", true},
		{"resume when not paused ignored", StatusDone, Event{Kind: EventResumed}, StatusDone, "", true},
		{"unknown kind ignored", StatusDone, Event{Kind: "Nope"}, StatusDone, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{Status: tt.from}
			tr := Apply(s, tt.event)
			if s.Status != tt.want {
				t.Errorf("status = %q, want %q", s.Status, tt.want)
			}
			if tt.applied && s.StatusDetail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", s.StatusDetail, tt.wantDetail)
			}
			if tr.From != tt.from || tr.To != tt.want {
				t.Errorf("transition = %v, want %q -> %q", tr, tt.from, tt.want)
			}
			if tr.Applied != tt.applied {
				t.Errorf("applied = %v, want %v", tr.Applied, tt.applied)
			}
		})
	}
}

func TestStaleEventsAreDropped(t *testing.T) {
	s := &Session{Status: StatusBusy}
	Apply(s, Event{Kind: HookStop, Time: at(10)})
	tr := Apply(s, Event{Kind: HookPreToolUse, Time: at(5)})
	if tr.Applied || s.Status != StatusDone {
		t.Fatalf("stale event applied: %+v, status %q", tr, s.Status)
	}
	if !s.LastEventAt.Equal(at(10)) {
		t.Errorf("LastEventAt = %v, want %v", s.LastEventAt, at(10))
	}
	// Same timestamp is not stale.
	if tr := Apply(s, Event{Kind: HookUserPromptSubmit, Time: at(10)}); !tr.Applied {
		t.Error("event with equal timestamp should apply")
	}
}

func TestSessionIDHistory(t *testing.T) {
	s := &Session{}
	Apply(s, Event{Kind: HookSessionStart, Source: "startup", SessionID: "a", Cwd: "/p", TranscriptPath: "/t/a"})
	Apply(s, Event{Kind: HookSessionStart, Source: "startup", SessionID: "a"}) // same id: no history entry
	Apply(s, Event{Kind: HookSessionEnd, Reason: "clear", SessionID: "a"})
	Apply(s, Event{Kind: HookSessionStart, Source: "clear", SessionID: "b", TranscriptPath: "/t/b"})
	Apply(s, Event{Kind: HookSessionStart, Source: "fork", SessionID: "c"})
	if s.ClaudeSessionID != "c" {
		t.Errorf("ClaudeSessionID = %q, want c", s.ClaudeSessionID)
	}
	if got := s.ClaudeHistory; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("ClaudeHistory = %v, want [a b]", got)
	}
	if s.TranscriptPath != "/t/b" || s.Cwd != "/p" {
		t.Errorf("transcript/cwd = %q/%q", s.TranscriptPath, s.Cwd)
	}
	if s.Status != StatusDone {
		t.Errorf("status = %q, want done", s.Status)
	}
}

func TestCwdChanged(t *testing.T) {
	s := &Session{Cwd: "/repo", Status: StatusBusy}
	Apply(s, Event{Kind: HookCwdChanged, Cwd: "/repo/.claude/worktrees/x"})
	if s.Cwd != "/repo/.claude/worktrees/x" || s.Status != StatusBusy {
		t.Errorf("cwd = %q status = %q", s.Cwd, s.Status)
	}
	Apply(s, Event{Kind: HookCwdChanged})
	if s.Cwd != "/repo/.claude/worktrees/x" {
		t.Error("empty cwd must not clear Cwd")
	}
}

func TestAutoNameFromFirstPrompt(t *testing.T) {
	s := &Session{}
	Apply(s, Event{Kind: HookUserPromptSubmit, Prompt: "fix the flaky e2e test\nmore details"})
	if s.Name != "fix the flaky e2e test" || s.NameSource != NameAuto {
		t.Errorf("name = %q (%s)", s.Name, s.NameSource)
	}
	Apply(s, Event{Kind: HookUserPromptSubmit, Prompt: "second prompt"})
	if s.Name != "fix the flaky e2e test" {
		t.Errorf("second prompt replaced name: %q", s.Name)
	}
	u := &Session{Name: "mine", NameSource: NameUser}
	Apply(u, Event{Kind: HookUserPromptSubmit, Prompt: "anything"})
	if u.Name != "mine" {
		t.Errorf("user name overwritten: %q", u.Name)
	}
}

func TestArchivedSessions(t *testing.T) {
	s := &Session{Status: StatusDone}
	Apply(s, Event{Kind: EventArchived, Time: at(1)})
	if s.Status != StatusArchived || !s.ArchivedAt.Equal(at(1)) {
		t.Fatalf("archive: %q at %v", s.Status, s.ArchivedAt)
	}
	for _, k := range []EventKind{HookStop, HookPreToolUse, HookNotification, EventAgentStatus, HookSessionEnd} {
		if tr := Apply(s, Event{Kind: k, Time: at(2)}); tr.Applied || s.Status != StatusArchived {
			t.Errorf("%s changed an archived session", k)
		}
	}
	Apply(s, Event{Kind: EventUnarchived, Time: at(3)})
	if s.Status != StatusPaused || !s.ArchivedAt.IsZero() {
		t.Errorf("unarchive: %q, ArchivedAt %v", s.Status, s.ArchivedAt)
	}

	// Using an archived (external) session again brings it back.
	e := &Session{Status: StatusArchived, ArchivedAt: at(1)}
	tr := Apply(e, Event{Kind: HookUserPromptSubmit, Time: at(4)})
	if e.Status != StatusBusy || !e.ArchivedAt.IsZero() || tr.From != StatusArchived {
		t.Errorf("reactivate: %q, %v, %+v", e.Status, e.ArchivedAt, tr)
	}
}

func TestNeedsAttention(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusWaiting: true, StatusDone: true, StatusError: true,
		StatusBusy: false, StatusStarting: false, StatusPaused: false, StatusArchived: false,
	} {
		if s.NeedsAttention() != want {
			t.Errorf("%s.NeedsAttention() = %v", s, !want)
		}
	}
	if (Transition{From: StatusBusy, To: StatusDone}).Changed() != true {
		t.Error("Changed")
	}
}

func TestAnyEventGivesTheTranscript(t *testing.T) {
	s := &Session{Status: StatusBusy, ClaudeSessionID: "c"}
	Apply(s, Event{Kind: HookPreToolUse, SessionID: "c", TranscriptPath: "/t.jsonl", Time: time.Unix(1, 0)})
	if s.TranscriptPath != "/t.jsonl" {
		t.Errorf("transcript %q", s.TranscriptPath)
	}
	Apply(s, Event{Kind: HookPreToolUse, SessionID: "other", TranscriptPath: "/other.jsonl", Time: time.Unix(2, 0)})
	if s.TranscriptPath != "/t.jsonl" {
		t.Errorf("another conversation's transcript was taken: %q", s.TranscriptPath)
	}
}
