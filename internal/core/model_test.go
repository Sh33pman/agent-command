package core

import "testing"

func TestNeedsResume(t *testing.T) {
	for _, tc := range []struct {
		s      Session
		resume bool
	}{
		{Session{Kind: KindHosted, Status: StatusPaused, ClaudeSessionID: "c"}, true},
		{Session{Kind: KindHosted, Status: StatusFinished, ClaudeSessionID: "c"}, true},
		{Session{Kind: KindHosted, Status: StatusDone, ClaudeSessionID: "c"}, false},
		{Session{Kind: KindHosted, Status: StatusPaused}, false}, // nothing to resume
	} {
		if tc.s.NeedsResume() != tc.resume {
			t.Errorf("%+v: resume=%v", tc.s, tc.s.NeedsResume())
		}
	}
	if (&Session{ID: "x"}).Ref() != "x" {
		t.Error("Ref")
	}
}
