package core

import "time"

// Transition is the result of applying an event to a session.
type Transition struct {
	From, To Status
	// Applied is true when the event was accepted: recognized and not
	// stale. Accepted events update LastEventAt (the "last activity" time)
	// even when the status doesn't change.
	Applied bool
}

// Changed reports whether the status changed.
func (t Transition) Changed() bool { return t.From != t.To }

// Apply feeds one event into the session's state machine and
// returns what happened. It never touches I/O. Setting Unseen and deciding
// on notifications is up to the caller, which knows which views show what.
func Apply(s *Session, e Event) Transition {
	t := Transition{From: s.Status, To: s.Status}

	// Order by event time; drop events older than the last one applied
	// (monotonic clocks aren't comparable across hook processes).
	if !e.Time.IsZero() && e.Time.Before(s.LastEventAt) {
		return t
	}

	if s.Status == StatusArchived {
		switch e.Kind {
		case EventUnarchived:
			s.ArchivedAt = time.Time{}
			set(s, &t, StatusPaused, "")
		case HookSessionStart, HookUserPromptSubmit:
			// The user is using an archived session again: bring it back.
			s.ArchivedAt = time.Time{}
			s.Status = StatusPaused
			return applyLive(s, e, t)
		default:
			return t
		}
		touch(s, e, &t)
		return t
	}
	return applyLive(s, e, t)
}

func applyLive(s *Session, e Event, t Transition) Transition {
	switch e.Kind {
	case EventCreated:
		set(s, &t, StatusStarting, "")

	case HookSessionStart:
		adoptIDs(s, e)
		if e.Source != "compact" {
			set(s, &t, StatusDone, "")
		}

	case HookCwdChanged:
		if e.Cwd != "" {
			s.Cwd = e.Cwd
		}

	case HookUserPromptSubmit:
		if s.NameSource != NameUser && s.Name == "" && !e.Wakeup {
			if n := NameFromPrompt(e.Prompt); n != "" {
				s.Name, s.NameSource = n, NameAuto
			}
		}
		set(s, &t, StatusBusy, "")

	case HookPreToolUse, HookPostToolUse, HookPostToolUseFailure, HookSubagentStart, HookPreCompact:
		// Work is happening; this also clears `waiting` once a permission
		// or question is answered.
		detail := ""
		if e.ToolName != "" && e.Kind == HookPreToolUse {
			detail = e.ToolName
		}
		set(s, &t, StatusBusy, detail)

	case HookSubagentStop, HookPostCompact:
		// No status information: a SubagentStop was recorded after the
		// main turn's Stop (M0 fixture), so it must not reopen `busy`.

	case HookPermissionRequest:
		set(s, &t, StatusWaiting, waitingDetail(e))

	case HookNotification:
		switch e.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			set(s, &t, StatusWaiting, waitingDetail(e))
		case "idle_prompt", "agent_completed":
			// Never overrides `waiting` (M0 A6); recovers a missed Stop.
			if s.Status == StatusBusy {
				set(s, &t, StatusDone, "")
			}
		}

	case HookStop:
		set(s, &t, StatusDone, "")

	case HookStopFailure:
		set(s, &t, StatusError, firstNonEmpty(e.Error, e.Message, "API error"))

	case EventInterrupted:
		// Esc fires no Stop (M0 A12). Denying a permission, or pressing Esc on a
		// question, interrupts too, so it ends waiting as well.
		if s.Status == StatusBusy || s.Status == StatusWaiting {
			set(s, &t, StatusDone, "")
		}

	case EventForeground:
		// Not touched: what's in front is seen now, and mustn't make a hook sent
		// a moment ago look stale.
		return applyFront(s, e, t)

	case EventAgentStatus:
		switch e.AgentStatus {
		case AgentBusy:
			set(s, &t, StatusBusy, s.StatusDetail)
		case AgentWaiting:
			if s.Status != StatusWaiting {
				set(s, &t, StatusWaiting, "")
			}
		case AgentIdle:
			if s.Status != StatusWaiting && s.Status != StatusError {
				set(s, &t, StatusDone, "")
			}
		case AgentStopped:
			if s.Status != StatusFinished { // finishing stops it: that's expected
				set(s, &t, StatusPaused, "stopped")
			}
		}

	case HookSessionEnd:
		switch e.Reason {
		case "clear", "resume":
			// A SessionStart follows in the same process.
		default:
			switch {
			case s.IsTerminal(): // the agent has gone, not the shell
				set(s, &t, StatusRunning, "")
				s.EndAgent()
			case s.Status != StatusFinished:
				set(s, &t, StatusPaused, e.Reason)
			}
		}

	case EventArchived:
		s.ArchivedAt = e.Time
		set(s, &t, StatusArchived, "")

	case EventFinished:
		set(s, &t, StatusFinished, "")

	case EventResumed:
		if s.Stopped() {
			set(s, &t, StatusStarting, "")
		}

	default:
		return t
	}
	touch(s, e, &t)
	return t
}

// applyFront is the fallback for a terminal's agent:
// the shell back in front means the agent has gone, even if it sent no
// SessionEnd (it crashed or was killed). An agent in front that has sent no
// status is unknown, rather than a guess. The daemon waits a few seconds before
// sending that, so its first hook has time to arrive.
func applyFront(s *Session, e Event, t Transition) Transition {
	if !s.IsTerminal() {
		return t
	}
	switch {
	case e.Front == FrontShell:
		switch s.Status {
		case StatusBusy, StatusWaiting, StatusDone, StatusError, StatusUnknown:
		default: // no agent, or the terminal isn't running
			return t
		}
		set(s, &t, StatusRunning, "")
		s.EndAgent()
	case s.Status == StatusRunning && s.ClaudeSessionID == "" && IsAgent(e.Front):
		set(s, &t, StatusUnknown, e.Front)
	}
	return t
}

// adoptIDs records Claude's current session id. A new id on the same acmd
// session (after /clear, a fork, or a move into the background) pushes the
// old one onto ClaudeHistory.
func adoptIDs(s *Session, e Event) {
	if e.SessionID != "" && e.SessionID != s.ClaudeSessionID {
		if s.ClaudeSessionID != "" {
			s.ClaudeHistory = append(s.ClaudeHistory, s.ClaudeSessionID)
		}
		s.ClaudeSessionID = e.SessionID
	}
	if e.TranscriptPath != "" {
		s.TranscriptPath = e.TranscriptPath
	}
	if e.Cwd != "" {
		s.Cwd = e.Cwd
	}
}

func set(s *Session, t *Transition, to Status, detail string) {
	s.Status = to
	s.StatusDetail = detail
	t.To = to
	t.Applied = true
}

func touch(s *Session, e Event, t *Transition) {
	t.Applied = true
	// Every hook says where the transcript is, so a missed SessionStart doesn't
	// stop Esc being noticed (§5.4).
	if e.TranscriptPath != "" && e.SessionID != "" && e.SessionID == s.ClaudeSessionID {
		s.TranscriptPath = e.TranscriptPath
	}
	if e.Time.After(s.LastEventAt) {
		s.LastEventAt = e.Time
	}
}

func waitingDetail(e Event) string {
	switch {
	case e.ToolName == "AskUserQuestion":
		return "question"
	case e.ToolName != "":
		return "permission: " + e.ToolName
	case e.Message != "":
		return e.Message
	default:
		return e.NotificationType
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
