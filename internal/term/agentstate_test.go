package term

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Captured from agy 1.5.9 starting in a folder it doesn't trust yet.
const (
	agyAuth       = "\x1b]7501;state=blocked:kind=auth:app=antigravity:msg=U2lnbiBpbiByZXF1aXJlZA==\x07"
	agyPermission = "\x1b]7501;state=blocked:kind=permission:app=antigravity:msg=VHJ1c3QgdGhpcyBmb2xkZXI/\x07"
)

func TestScanReadsAgyReports(t *testing.T) {
	var s oscAgentScanner
	got := s.scan([]byte("hello" + agyAuth + "\x1b[2Jdrawing" + agyPermission + "more"))
	if len(got) != 2 {
		t.Fatalf("got %d reports: %+v", len(got), got)
	}
	if g := got[0]; g.State != "blocked" || g.Kind != "auth" || g.App != "antigravity" || g.Msg != "Sign in required" {
		t.Errorf("first = %+v", g)
	}
	if g := got[1]; g.Kind != "permission" || g.Msg != "Trust this folder?" {
		t.Errorf("second = %+v", g)
	}
}

// A report can arrive in pieces: split it at every byte.
func TestScanJoinsAReportSplitAcrossReads(t *testing.T) {
	whole := "x" + agyPermission + "y"
	for i := 1; i < len(whole); i++ {
		var s oscAgentScanner
		got := append(s.scan([]byte(whole[:i])), s.scan([]byte(whole[i:]))...)
		if len(got) != 1 || got[0].Msg != "Trust this folder?" {
			t.Fatalf("split at %d: %+v", i, got)
		}
	}
}

func TestScanOtherForms(t *testing.T) {
	var s oscAgentScanner
	for _, c := range []struct {
		in   string
		want []string // states
	}{
		{"\x1b]7501;state=working:app=antigravity\x1b\\", []string{"working"}}, // ESC \ ends it too
		{"\x1b]7501;?\x07", nil},                                           // agy's support check
		{"\x1b]7501;state=sleeping\x07", nil},                              // not a state
		{"\x1b]7501;state=failed:msg=not base64!\x07", []string{"failed"}}, // message dropped
		{"\x1b]0;title\x07\x1b]7501;state=done\x07", []string{"done"}},     // beside other OSCs
		{"\x1b]7501;state=idle\x07\x1b]7501;state=working\x07", []string{"idle", "working"}},
	} {
		var states []string
		for _, r := range s.scan([]byte(c.in)) {
			states = append(states, r.State)
		}
		if len(states) != len(c.want) || (len(states) > 0 && states[0] != c.want[0]) || (len(states) > 1 && states[1] != c.want[1]) {
			t.Errorf("%q: %v, want %v", c.in, states, c.want)
		}
	}
}

// Bytes that start a report but never end it aren't kept without limit.
func TestScanDropsAnEndlessReport(t *testing.T) {
	var s oscAgentScanner
	s.scan([]byte("\x1b]7501;state=working:msg="))
	big := make([]byte, maxOSCAgent+10)
	for i := range big {
		big[i] = 'A'
	}
	s.scan(big)
	if len(s.carry) > maxOSCAgent+len(oscAgentPrefix) {
		t.Errorf("kept %d bytes", len(s.carry))
	}
	if got := s.scan([]byte(agyAuth)); len(got) != 1 {
		t.Errorf("after an endless one, the next report: %+v", got)
	}
}

// The host keeps the last report and answers it, with a count that goes up.
func TestHostReportsTheAgentState(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdterm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	ready := make(chan struct{})
	s := &Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"},
		Ready: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	<-ready

	if st, ok := AgentStatus(sock); !ok || st.Seq != 0 {
		t.Fatalf("before any report: %+v %v", st, ok)
	}
	// printf turns \033 and \007 into ESC and BEL; the shell echoes the command
	// itself without them, so only printf's output is a report.
	if err := Send(sock, []byte(`printf '\033]7501;state=blocked:kind=question:msg=V2hpY2ggb25lPw==\007'`+"\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the report", func() bool { st, ok := AgentStatus(sock); return ok && st.Seq == 1 })
	st, _ := AgentStatus(sock)
	if st.State != "blocked" || st.Kind != "question" || st.Msg != "Which one?" || st.At.IsZero() || time.Since(st.At) > time.Minute {
		t.Errorf("state = %+v", st)
	}
}
