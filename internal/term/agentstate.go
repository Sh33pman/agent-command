package term

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"strings"
	"time"
)

// AgentState is what an agent in the session last reported about itself over
// OSC 7501, as Antigravity's CLI (agy) does:
//
//	ESC ] 7501 ; state=blocked:kind=permission:app=antigravity:msg=<base64> BEL
//
// State is working, blocked (on you: a permission, a question, a plan, a sign
// in), idle, done or failed. Seq goes up with each report, so a reader can
// tell a new one from the one it saw last; 0 means none yet.
type AgentState struct {
	State string    `json:"state"`
	Kind  string    `json:"kind,omitempty"`
	App   string    `json:"app,omitempty"`
	Msg   string    `json:"msg,omitempty"`
	Seq   uint64    `json:"seq"`
	At    time.Time `json:"at"`
}

const (
	oscAgentPrefix = "\x1b]7501;"
	// maxOSCAgent bounds a sequence the scanner keeps waiting for: past it, the
	// bytes aren't a report. A done report carries agy's whole reply, base64.
	maxOSCAgent = 128 << 10
	// maxAgentMsg is the most of a report's message kept, as for Claude's last
	// message.
	maxAgentMsg = 64 << 10
)

// oscAgentScanner finds OSC 7501 reports in a program's output. A report can be
// split across reads, so the start of an unfinished one is kept for the next.
type oscAgentScanner struct {
	carry []byte
}

// scan reads p and returns the reports in it, in order.
func (s *oscAgentScanner) scan(p []byte) []AgentState {
	buf := p
	if len(s.carry) > 0 {
		buf = append(append([]byte(nil), s.carry...), p...)
		s.carry = nil
	}
	var out []AgentState
	for {
		i := bytes.Index(buf, []byte(oscAgentPrefix))
		if i < 0 {
			// Keep a tail that could be the start of a prefix.
			s.carry = append(s.carry[:0], partialPrefix(buf)...)
			return out
		}
		body := buf[i+len(oscAgentPrefix):]
		end, termLen := oscEnd(body)
		if end < 0 {
			if len(body) <= maxOSCAgent {
				s.carry = append([]byte(nil), buf[i:]...)
			}
			return out
		}
		if st, ok := parseAgentState(string(body[:end])); ok {
			out = append(out, st)
		}
		buf = body[end+termLen:]
	}
}

// oscEnd finds the end of an OSC body: BEL, or ESC \.
func oscEnd(b []byte) (end, termLen int) {
	for i, c := range b {
		switch {
		case c == 0x07:
			return i, 1
		case c == 0x1b && i+1 < len(b) && b[i+1] == '\\':
			return i, 2
		case c == 0x1b && i+1 == len(b):
			return -1, 0 // the terminator may be split
		}
	}
	return -1, 0
}

// partialPrefix is the end of b that could begin oscAgentPrefix.
func partialPrefix(b []byte) []byte {
	for n := min(len(oscAgentPrefix)-1, len(b)); n > 0; n-- {
		if strings.HasPrefix(oscAgentPrefix, string(b[len(b)-n:])) {
			return b[len(b)-n:]
		}
	}
	return nil
}

// parseAgentState reads "state=…:kind=…:app=…:msg=<base64>". The query a CLI
// sends to check for support ("?") isn't a report.
func parseAgentState(body string) (AgentState, bool) {
	var st AgentState
	for _, field := range strings.Split(body, ":") {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "state":
			st.State = val
		case "kind":
			st.Kind = val
		case "app":
			st.App = val
		case "msg":
			if b, err := base64.StdEncoding.DecodeString(val); err == nil {
				st.Msg = clip(string(b), maxAgentMsg)
			}
		}
	}
	switch st.State {
	case "working", "blocked", "idle", "done", "failed":
		return st, true
	}
	return AgentState{}, false
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// AgentStatus asks a session's host for the agent's last OSC 7501 report. ok is
// false when the host can't be reached; a host that has seen none returns Seq 0.
func AgentStatus(socket string) (st AgentState, ok bool) {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return AgentState{}, false
	}
	defer c.Close()
	if err := writeFrame(c, frameAgent, nil); err != nil {
		return AgentState{}, false
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b, err := io.ReadAll(c)
	if err != nil || len(b) == 0 || json.Unmarshal(b, &st) != nil {
		return AgentState{}, false
	}
	return st, true
}
