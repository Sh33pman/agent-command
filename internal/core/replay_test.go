package core

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Replays the real hook sequences recorded in M0 (testdata/hooks, see
// spikes/FINDINGS.md §2) through the state machine.

type fixtureLine struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
	TS      float64         `json:"ts"`
}

type replayed struct {
	ev   Event
	file string
}

func loadFixture(t *testing.T, name string) []replayed {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []replayed
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxHookPayload)
	for sc.Scan() {
		var l fixtureLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sec, frac := math.Modf(l.TS)
		ev, err := ParseHook(l.Event, l.Payload, time.Unix(int64(sec), int64(frac*1e9)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, replayed{ev, name})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func replay(evs []replayed) map[string]*Session {
	sessions := map[string]*Session{}
	for _, r := range evs {
		s := sessions[r.ev.SessionID]
		if s == nil {
			s = &Session{}
			sessions[r.ev.SessionID] = s
		}
		Apply(s, r.ev)
	}
	return sessions
}

// Fake session ids in the fixtures are remapped like 00000006-0000-4000-8000-000000000006.
func sid(n int) string {
	return (map[int]string{
		1: "00000001-0000-4000-8000-000000000001",
		2: "00000002-0000-4000-8000-000000000002",
		4: "00000004-0000-4000-8000-000000000004",
		6: "00000006-0000-4000-8000-000000000006",
	})[n]
}

func TestReplayFixtures(t *testing.T) {
	tests := []struct {
		file string
		want map[int]Status
		// detail checks for specific sessions
		detail map[int]string
	}{
		{"interactive-startup.jsonl", map[int]Status{1: StatusDone}, nil},
		{"bg-unattached-two-sessions.jsonl", map[int]Status{2: StatusDone, 4: StatusDone}, nil},
		// A2/A6: a background session asking a question blocks while unattached.
		{"bg-question-blocks-unattached.jsonl", map[int]Status{6: StatusWaiting}, map[int]string{6: "Claude needs your permission"}},
		// A7: closing the pane fires SessionEnd(other); the question was answered and the turn finished.
		{"interactive-pane-closed.jsonl", map[int]Status{1: StatusPaused, 6: StatusDone}, nil},
		// A12: hooks alone leave an Esc-interrupted turn busy (the gap the transcript tail closes).
		{"attached-answer-then-esc-interrupt.jsonl", map[int]Status{6: StatusBusy}, map[int]string{6: "Bash"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := replay(loadFixture(t, tt.file))
			for n, want := range tt.want {
				s := got[sid(n)]
				if s == nil {
					t.Fatalf("session %d not in fixture", n)
				}
				if s.Status != want {
					t.Errorf("session %d: status %q, want %q", n, s.Status, want)
				}
				if d, ok := tt.detail[n]; ok && s.StatusDetail != d {
					t.Errorf("session %d: detail %q, want %q", n, s.StatusDetail, d)
				}
			}
		})
	}
}

// The fixtures were recorded back to back with consistent ids, so replaying
// them all in time order reproduces experiment A end to end.
func TestReplayWholeTimeline(t *testing.T) {
	var all []replayed
	for _, f := range []string{
		"interactive-startup.jsonl", "bg-unattached-two-sessions.jsonl",
		"bg-question-blocks-unattached.jsonl", "interactive-pane-closed.jsonl",
		"attached-answer-then-esc-interrupt.jsonl",
	} {
		all = append(all, loadFixture(t, f)...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ev.Time.Before(all[j].ev.Time) })

	got := replay(all)
	want := map[int]Status{1: StatusPaused, 2: StatusDone, 4: StatusDone, 6: StatusBusy}
	for n, w := range want {
		if s := got[sid(n)]; s == nil || s.Status != w {
			t.Errorf("session %d: %+v, want %q", n, s, w)
		}
	}

	// The transcript tail then sees "[Request interrupted by user" (§5.4).
	s := got[sid(6)]
	Apply(s, Event{Kind: EventInterrupted, Time: s.LastEventAt.Add(time.Second)})
	if s.Status != StatusDone {
		t.Errorf("after interrupt marker: %q, want done", s.Status)
	}
	// And the first prompt of session 6 gave it an auto-name.
	if s.Name == "" || s.NameSource != NameAuto {
		t.Errorf("session 6 auto-name = %q (%s)", s.Name, s.NameSource)
	}
}
