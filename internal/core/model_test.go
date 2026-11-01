package core

import "testing"

func TestManagedAndNeedsResume(t *testing.T) {
	for _, tc := range []struct {
		s       Session
		managed bool
		resume  bool
	}{
		{Session{Kind: KindHosted, Status: StatusPaused, ClaudeSessionID: "c"}, true, true},
		{Session{Kind: KindHosted, Status: StatusDone, ClaudeSessionID: "c"}, true, false},
		{Session{Kind: KindHosted, Status: StatusPaused}, true, false},
		{Session{Kind: KindBackground, Status: StatusPaused, ClaudeSessionID: "c"}, true, true},
		{Session{Kind: KindBackground, BgID: "b", Status: StatusDone, ClaudeSessionID: "c"}, true, true}, // from before hosting
		{Session{Kind: KindExternal, Status: StatusPaused, ClaudeSessionID: "c"}, false, false},
	} {
		if tc.s.Managed() != tc.managed || tc.s.NeedsResume() != tc.resume {
			t.Errorf("%+v: managed=%v resume=%v", tc.s, tc.s.Managed(), tc.s.NeedsResume())
		}
	}
	if (&Session{ID: "x"}).Ref() != "x" || (&Session{ID: "x", BgID: "b"}).Ref() != "b" {
		t.Error("Ref")
	}
}
