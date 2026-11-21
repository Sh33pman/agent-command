package core

import (
	"slices"
	"strings"
	"testing"
)

func TestAgent(t *testing.T) {
	cases := []struct {
		s    Session
		want string
	}{
		{Session{Harness: HarnessClaude}, AgentClaude},
		{Session{}, AgentClaude}, // older sessions have no harness
		{Session{Harness: HarnessAntigravity}, AgentAntigravity},
		{Session{Harness: HarnessTerminal, Status: StatusRunning}, AgentNone},
		{Session{Harness: HarnessTerminal, Status: StatusBusy, ClaudeSessionID: "c1"}, AgentClaude},
		{Session{Harness: HarnessTerminal, Status: StatusUnknown, StatusDetail: "claude"}, AgentClaude},
		{Session{Harness: HarnessTerminal, Status: StatusUnknown, StatusDetail: "agy"}, AgentAntigravity},
		{Session{Harness: HarnessTerminal, Status: StatusUnknown, StatusDetail: "codex"}, AgentOther},
		{Session{Harness: HarnessTerminal, Status: StatusUnknown}, AgentNone},
	}
	for _, c := range cases {
		if got := c.s.Agent(); got != c.want {
			t.Errorf("%+v: agent %q, want %q", c.s, got, c.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if (&Session{Name: "api", Cwd: "/w/x"}).DisplayName() != "api" || (&Session{Cwd: "/w/web"}).DisplayName() != "web" ||
		(&Session{}).DisplayName() != "" {
		t.Error("display name")
	}
}

func caps(s Session) string { return strings.Join(s.Capabilities(), ",") }

func TestCapabilities(t *testing.T) {
	running := Session{Harness: HarnessClaude, Status: StatusBusy, Cwd: "/w", ClaudeSessionID: "c1"}
	if got := caps(running); got != "attach,archive,continue_in_job,open_in_editor,set_remote" {
		t.Errorf("running = %s", got)
	}
	stopped := running
	stopped.Status = StatusPaused
	if !slices.Contains(stopped.Capabilities(), CanResume) {
		t.Errorf("stopped = %s", caps(stopped))
	}
	archived := running
	archived.Status = StatusArchived
	if got := caps(archived); got != "unarchive,purge,open_in_editor" {
		t.Errorf("archived = %s", got)
	}
	if got := caps(Session{Harness: HarnessTerminal, Status: StatusArchived}); got != "purge" {
		t.Errorf("an archived terminal = %s", got)
	}
	if !slices.Contains((&Session{Harness: HarnessClaude, Status: StatusStarting}).Capabilities(), CanContinueInJob) {
		t.Error("a Claude session before its first hook")
	}
	if slices.Contains((&Session{Harness: HarnessTerminal, Status: StatusUnknown, StatusDetail: "claude"}).Capabilities(), CanContinueInJob) {
		t.Error("Claude typed in a terminal, with no conversation yet")
	}
	terminal := Session{Harness: HarnessTerminal, Status: StatusRunning, Cwd: "/w"}
	if slices.Contains(terminal.Capabilities(), CanContinueInJob) {
		t.Error("a terminal with no agent continues a job")
	}
	agy := Session{Harness: HarnessAntigravity, Status: StatusDone, Cwd: "/w"}
	if slices.Contains(agy.Capabilities(), CanContinueInJob) {
		t.Error("an agy session with no conversation yet")
	}
	onHost := Session{Harness: HarnessClaude, Status: StatusPaused, Host: "studio", Cwd: "/w", ClaudeSessionID: "c1"}
	if got := caps(onHost); got != "attach,archive,remove_added" {
		t.Errorf("a host's = %s", got)
	}
	onHost.HostGone = true
	if got := caps(onHost); got != "remove_added" {
		t.Errorf("gone from its host = %s", got)
	}
	if got := (&Session{Status: StatusArchived, Host: "h"}).Capabilities(); got == nil || len(got) != 1 {
		t.Errorf("caps = %v", got)
	}
	if got := (&Session{Status: StatusArchived, Host: "h", HostGone: true}).Capabilities(); got == nil {
		t.Error("nil, not an empty list")
	}
}
