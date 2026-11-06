//go:build libghostty

package vt

import (
	"fmt"
	"strings"
	"testing"
)

const testScrollback = 10 << 20

func mustNew(t *testing.T, cols, rows int, reply func([]byte)) *Terminal {
	t.Helper()
	v, err := New(cols, rows, testScrollback, reply)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return v
}

func snapshot(t *testing.T, v *Terminal) []byte {
	t.Helper()
	b, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func text(t *testing.T, v *Terminal) string {
	t.Helper()
	s, err := v.Text()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A snapshot replayed into a new terminal must recreate the same state, shown
// by that terminal producing an identical snapshot and identical text.
func TestSnapshotRoundTrip(t *testing.T) {
	var history strings.Builder
	for i := range 500 {
		fmt.Fprintf(&history, "\x1b[3%dmline %d\x1b[0m\r\n", i%8, i)
	}
	cases := map[string]string{
		"plain":           "hello\r\nworld",
		"scrollback":      history.String(),
		"styled cursor":   "\x1b[1;31mred\x1b[0m\x1b[5;10H\x1b[4munderlined here",
		"wide characters": "日本語 and emoji 🎉\r\né combining",
		"modes":           "\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[?25l text",
		"kitty keyboard":  "\x1b[>1u kitty",
		"scroll region":   "\x1b[5;20r\x1b[10;1Hinside",
		"hyperlink":       "\x1b]8;;https://example.com\x07link\x1b]8;;\x07 after",
		"title and pwd":   "\x1b]7;file://host/tmp\x07prompt",
		"alternate":       "primary\x1b[?1049h\x1b[2J\x1b[3;3Halternate",
		"soft wrap":       strings.Repeat("x", 200),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			a := mustNew(t, 80, 24, nil)
			a.Write([]byte(input))
			snap := snapshot(t, a)

			b := mustNew(t, 80, 24, nil)
			b.Write(snap)
			if got, want := text(t, b), text(t, a); got != want {
				t.Errorf("text differs\n got %q\nwant %q", got, want)
			}
			if got := snapshot(t, b); string(got) != string(snap) {
				t.Errorf("state differs\n got %q\nwant %q", got, snap)
			}
		})
	}
}

// Output that keeps arriving after a snapshot must land as it would have on
// the original terminal.
func TestOutputAfterSnapshot(t *testing.T) {
	a := mustNew(t, 40, 10, nil)
	a.Write([]byte("\x1b[1;32mgreen \x1b[3;5Hplaced"))
	b := mustNew(t, 40, 10, nil)
	b.Write(snapshot(t, a))

	more := []byte(" more\r\n\x1b[0mnext line")
	a.Write(more)
	b.Write(more)
	if got, want := snapshot(t, b), snapshot(t, a); string(got) != string(want) {
		t.Errorf("state differs\n got %q\nwant %q", got, want)
	}
}

func TestRepliesToQueries(t *testing.T) {
	var got []byte
	v := mustNew(t, 80, 24, func(b []byte) { got = append(got, b...) })
	v.Write([]byte("\x1b[3;7H\x1b[6n"))
	if want := "\x1b[3;7R"; string(got) != want {
		t.Errorf("cursor report = %q, want %q", got, want)
	}
}

func TestSequenceSplitAcrossWrites(t *testing.T) {
	v := mustNew(t, 80, 24, nil)
	v.Write([]byte("a\x1b[3"))
	v.Write([]byte("1mb"))
	if got := text(t, v); got != "ab" {
		t.Errorf("text = %q, want %q", got, "ab")
	}
}

func TestResizeKeepsContent(t *testing.T) {
	v := mustNew(t, 80, 24, nil)
	v.Write([]byte("keep me"))
	if err := v.Resize(40, 10); err != nil {
		t.Fatal(err)
	}
	if got := text(t, v); got != "keep me" {
		t.Errorf("text = %q", got)
	}
	if err := v.Resize(0, 10); err == nil {
		t.Error("Resize(0, 10) should fail")
	}
}

func TestInvalidSize(t *testing.T) {
	if _, err := New(0, 24, testScrollback, nil); err == nil {
		t.Error("New(0, 24) should fail")
	}
}

func TestScrollbackIsLimited(t *testing.T) {
	v, err := New(80, 24, 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	line := []byte(strings.Repeat("y", 79) + "\r\n")
	for range 200_000 {
		v.Write(line)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) > 8<<20 {
		t.Errorf("snapshot is %d bytes with a 1 MiB scrollback limit", len(snap))
	}
}

func BenchmarkWrite(b *testing.B) {
	v, err := New(120, 40, testScrollback, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer v.Close()
	chunk := []byte(strings.Repeat("\x1b[32mhello world\x1b[0m output line\r\n", 1000))
	b.SetBytes(int64(len(chunk)))
	for b.Loop() {
		v.Write(chunk)
	}
}

// A restored terminal must itself report the state, so a snapshot that dropped
// something on both sides can't pass by comparing equal.
func TestRestoredStateIsReported(t *testing.T) {
	cases := []struct{ name, input, query, want string }{
		{"mouse tracking", "\x1b[?1000h", "\x1b[?1000$p", "\x1b[?1000;1$y"},
		{"sgr mouse", "\x1b[?1006h", "\x1b[?1006$p", "\x1b[?1006;1$y"},
		{"bracketed paste", "\x1b[?2004h", "\x1b[?2004$p", "\x1b[?2004;1$y"},
		{"synchronized output off", "", "\x1b[?2026$p", "\x1b[?2026;2$y"},
		{"hidden cursor", "\x1b[?25l", "\x1b[?25$p", "\x1b[?25;2$y"},
		{"focus events", "\x1b[?1004h", "\x1b[?1004$p", "\x1b[?1004;1$y"},
		{"alternate screen", "\x1b[?1049h", "\x1b[?1049$p", "\x1b[?1049;1$y"},
		{"kitty keyboard", "\x1b[>1u", "\x1b[?u", "\x1b[?1u"},
		{"cursor position", "\x1b[7;12H", "\x1b[6n", "\x1b[7;12R"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := mustNew(t, 80, 24, nil)
			a.Write([]byte(c.input))
			var got []byte
			b := mustNew(t, 80, 24, func(p []byte) { got = append(got, p...) })
			b.Write(snapshot(t, a))
			got = nil // replies caused by the snapshot itself are checked below
			b.Write([]byte(c.query))
			if string(got) != c.want {
				t.Errorf("reply = %q, want %q", got, c.want)
			}
		})
	}
}

// Replaying a snapshot must not make the terminal answer anything: on a real
// surface such replies would be typed into the program.
func TestSnapshotCausesNoReplies(t *testing.T) {
	a := mustNew(t, 80, 24, nil)
	a.Write([]byte("\x1b[?1000h\x1b[>1u\x1b[6n\x1b[c text"))
	var got []byte
	b := mustNew(t, 80, 24, func(p []byte) { got = append(got, p...) })
	b.Write(snapshot(t, a))
	if len(got) != 0 {
		t.Errorf("replay produced replies %q", got)
	}
}

func TestSnapshotKeepsScrollback(t *testing.T) {
	a := mustNew(t, 80, 24, nil)
	for i := range 500 {
		fmt.Fprintf(writerFunc(a.Write), "line %d\r\n", i)
	}
	b := mustNew(t, 80, 24, nil)
	b.Write(snapshot(t, a))
	s := text(t, b)
	for _, want := range []string{"line 0\n", "line 250\n", "line 499"} {
		if !strings.Contains(s, want) {
			t.Errorf("restored text is missing %q", want)
		}
	}
}

// What happens to the primary screen when a client attaches while a program
// has the alternate screen open, then leaves it.
func TestPrimaryScreenBehindAlternate(t *testing.T) {
	a := mustNew(t, 80, 24, nil)
	a.Write([]byte("primary text\r\n\x1b[?1049h\x1b[2Jalternate text"))
	b := mustNew(t, 80, 24, nil)
	b.Write(snapshot(t, a))
	leave := []byte("\x1b[?1049l")
	a.Write(leave)
	b.Write(leave)
	if got, want := text(t, b), text(t, a); got != want {
		t.Errorf("after leaving the alternate screen\n got %q\nwant %q", got, want)
	}
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) {
	f(p)
	return len(p), nil
}

// Taking a snapshot must not change the terminal.
func TestSnapshotLeavesStateAlone(t *testing.T) {
	v := mustNew(t, 80, 24, nil)
	v.Write([]byte("primary\r\n\x1b[5;5H\x1b[?1049h\x1b[2J\x1b[3;3Halternate\x1b[?1000h"))
	first := snapshot(t, v)
	if second := snapshot(t, v); string(second) != string(first) {
		t.Errorf("second snapshot differs\n got %q\nwant %q", second, first)
	}
	if got := text(t, v); got != "\n\n  alternate" {
		t.Errorf("active screen text = %q", got)
	}
	v.Write([]byte("\x1b[?1049l"))
	if got := text(t, v); got != "primary" {
		t.Errorf("primary text after leaving = %q", got)
	}
}

// Leaving the alternate screen must restore the cursor the program saved.
func TestCursorAfterLeavingAlternate(t *testing.T) {
	a := mustNew(t, 80, 24, nil)
	a.Write([]byte("primary\x1b[5;5H\x1b[?1049h\x1b[2J\x1b[3;3Halternate"))
	var got []byte
	b := mustNew(t, 80, 24, func(p []byte) { got = append(got, p...) })
	b.Write(snapshot(t, a))
	b.Write([]byte("\x1b[?1049l\x1b[6n"))
	if want := "\x1b[5;5R"; string(got) != want {
		t.Errorf("cursor after leaving = %q, want %q", got, want)
	}
}

// A snapshot must not set colors: the app's theme decides those.
func TestSnapshotLeavesColorsToTheClient(t *testing.T) {
	v := mustNew(t, 80, 24, nil)
	v.Write([]byte("text"))
	if s := snapshot(t, v); strings.Contains(string(s), "\x1b]4;") {
		t.Errorf("snapshot sets the palette: %q", s)
	}
}

// A chunk of output can end inside a sequence. The rest of it must still work
// on a client that attached in between.
func TestSnapshotMidSequence(t *testing.T) {
	for name, start := range map[string]string{
		"primary":   "abc\x1b[3",
		"alternate": "abc\x1b[?1049h\x1b[2Jdef\x1b[3",
		"osc":       "abc\x1b]7;file://ho",
		"utf8":      "abc\xe6\x97",
	} {
		t.Run(name, func(t *testing.T) {
			rest := map[string]string{"osc": "st/tmp\x07x", "utf8": "\xa5x"}[name]
			if rest == "" {
				rest = "1mred"
			}
			a := mustNew(t, 80, 24, nil)
			a.Write([]byte(start))
			b := mustNew(t, 80, 24, nil)
			b.Write(snapshot(t, a))
			a.Write([]byte(rest))
			b.Write([]byte(rest))
			if got, want := text(t, b), text(t, a); got != want {
				t.Errorf("text differs\n got %q\nwant %q", got, want)
			}
			if got, want := snapshot(t, b), snapshot(t, a); string(got) != string(want) {
				t.Errorf("state differs\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestPrimaryHistoryBehindAlternate(t *testing.T) {
	a := mustNew(t, 80, 24, nil)
	for i := range 300 {
		fmt.Fprintf(writerFunc(a.Write), "line %d\r\n", i)
	}
	a.Write([]byte("\x1b[?1049h\x1b[2Jfull screen"))
	b := mustNew(t, 80, 24, nil)
	b.Write(snapshot(t, a))
	if got, want := text(t, b), text(t, a); got != want {
		t.Errorf("active screen\n got %q\nwant %q", got, want)
	}
	b.Write([]byte("\x1b[?1049l"))
	s := text(t, b)
	for _, want := range []string{"line 0\n", "line 299"} {
		if !strings.Contains(s, want) {
			t.Errorf("history is missing %q", want)
		}
	}
}

// What programs get while no client is attached. Color, color scheme and window
// size queries have no answer yet: they need the client's theme and callbacks.
func TestQueriesAnsweredWithoutCallbacks(t *testing.T) {
	for name, c := range map[string]struct{ query, want string }{
		"primary device attributes":   {"\x1b[c", "\x1b[?62;22c"},
		"secondary device attributes": {"\x1b[>c", "\x1b[>1;0;0c"},
		"xtversion":                   {"\x1b[>q", "\x1bP>|libghostty\x1b\\"},
		"status report":               {"\x1b[5n", "\x1b[0n"},
		"kitty keyboard":              {"\x1b[?u", "\x1b[?0u"},
	} {
		t.Run(name, func(t *testing.T) {
			var got []byte
			v := mustNew(t, 80, 24, func(p []byte) { got = append(got, p...) })
			v.Write([]byte(c.query))
			if string(got) != c.want {
				t.Errorf("reply = %q, want %q", got, c.want)
			}
		})
	}
}
