// Package kittytest simulates enough of Kitty's remote control for tests
// of acmd's Kitty adapter and the commands built on it.
package kittytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/brettmostert/agent-command/internal/kitty"
)

// Sim is a tiny Kitty: one OS window, tabs of windows, enough of `kitten @`
// for the adapter. Resizes are off by the border (M0 K2).
type Sim struct {
	Tabs    []*kitty.Tab
	next    int
	Focused int
	Calls   []string
	Fail    string // command that fails
	FailErr error  // its error (default "<command> failed")
	Stuck   bool   // resizes change nothing
	Launch  string // override launch output
}

// New returns a Kitty with one tab (layout fat) holding windows 1 and 2;
// window 1 is focused.
func New() *Sim {
	s := &Sim{next: 10}
	s.Tabs = []*kitty.Tab{{ID: 1, Layout: "fat", Windows: []kitty.Window{{ID: 1, Columns: 120}, {ID: 2, Columns: 60}}}}
	s.Focused = 1
	return s
}

// Kitty returns an adapter talking to the simulation.
func (s *Sim) Kitty() *kitty.Kitty {
	return &kitty.Kitty{Kitten: "/k/kitten", To: "unix:/tmp/k", Run: s.Run}
}

func (s *Sim) find(id int) (*kitty.Tab, int) {
	for _, t := range s.Tabs {
		for i, w := range t.Windows {
			if w.ID == id {
				return t, i
			}
		}
	}
	return nil, -1
}

// Run plays `kitten @`.
func (s *Sim) Run(_ context.Context, kitten, to string, args ...string) ([]byte, error) {
	if kitten != "/k/kitten" || to != "unix:/tmp/k" {
		return nil, fmt.Errorf("wrong kitten/socket %s %s", kitten, to)
	}
	s.Calls = append(s.Calls, strings.Join(args, " "))
	if args[0] == s.Fail {
		if s.FailErr != nil {
			return nil, s.FailErr
		}
		return nil, errors.New(args[0] + " failed")
	}
	id := 0
	for i, a := range args {
		if a == "--match" {
			id, _ = strconv.Atoi(strings.TrimPrefix(args[i+1], "id:"))
		}
	}
	t, i := s.find(id)
	switch args[0] {
	case "ls":
		for _, t := range s.Tabs {
			for j := range t.Windows {
				t.Windows[j].IsFocused = t.Windows[j].ID == s.Focused
			}
		}
		return json.Marshal([]kitty.OSWindow{{ID: 1, Tabs: derefTabs(s.Tabs)}})
	case "goto-layout":
		t.Layout = args[len(args)-1]
	case "launch":
		if s.Launch != "" {
			return []byte(s.Launch), nil
		}
		s.next++
		w := kitty.Window{ID: s.next, Columns: t.Windows[i].Columns / 2, UserVars: map[string]string{}}
		t.Windows[i].Columns -= w.Columns
		for j, a := range args {
			if a == "--var" {
				k, v, _ := strings.Cut(args[j+1], "=")
				w.UserVars[k] = v
			}
		}
		t.Windows = append(t.Windows, w) // vsplit: on the right
		return []byte(strconv.Itoa(w.ID) + "\n"), nil
	case "focus-window":
		s.Focused = id
	case "action":
		switch args[3] {
		case "layout_action":
			if s.Focused != id {
				return nil, errors.New("move_to_screen_edge needs the window focused")
			}
			w := t.Windows[i]
			t.Windows = append([]kitty.Window{w}, append(t.Windows[:i:i], t.Windows[i+1:]...)...)
		case "toggle_layout":
			if t.Layout == "stack" {
				t.Layout = "splits"
			} else {
				t.Layout = "stack"
			}
		}
	case "resize-window":
		if s.Stuck {
			break
		}
		inc, _ := strconv.Atoi(args[len(args)-1])
		if inc > 0 {
			inc += 2 // borders
		}
		t.Windows[i].Columns += inc
	case "close-window":
		t.Windows = append(t.Windows[:i], t.Windows[i+1:]...)
	}
	return nil, nil
}

func derefTabs(ts []*kitty.Tab) []kitty.Tab {
	out := make([]kitty.Tab, len(ts))
	for i, t := range ts {
		out[i] = *t
	}
	return out
}
