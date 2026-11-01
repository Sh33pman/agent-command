package speech

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEngine answers like engine.py: ping, synth (a tiny file) and
// stream (one frame of 4 bytes per line of text). slow > 0 delays frames.
func fakeEngine(t *testing.T, sock string, texts *[]string, mu *sync.Mutex, slow time.Duration) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadBytes('\n')
				var req map[string]any
				_ = json.Unmarshal(line, &req)
				reply := map[string]any{"ok": true}
				switch req["op"] {
				case "synth":
					_ = os.WriteFile(req["out"].(string), []byte("RIFF"), 0o600)
					reply["seconds"] = 1.5
				case "stream":
					_, _ = c.Write([]byte(`{"ok":true,"rate":24000}` + "\n"))
					for _, l := range strings.Split(req["text"].(string), "\n") {
						mu.Lock()
						*texts = append(*texts, l)
						mu.Unlock()
						time.Sleep(slow)
						if _, err := c.Write([]byte{0, 0, 0, 4, 1, 2, 3, 4}); err != nil {
							return // the listener stopped
						}
					}
					_, _ = c.Write([]byte{0, 0, 0, 0})
					return
				}
				b, _ := json.Marshal(reply)
				_, _ = c.Write(append(b, '\n'))
			}()
		}
	}()
}

// fakePlayer records what it's given.
type fakePlayer struct {
	mu      sync.Mutex
	bytes   int
	drained bool
	closed  bool
}

func (p *fakePlayer) Write(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bytes += len(b)
	return nil
}
func (p *fakePlayer) Drain(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drained = true
	return nil
}
func (p *fakePlayer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

func testEngine(t *testing.T) *Engine {
	dir, err := os.MkdirTemp("", "acmdspk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return &Engine{Dir: filepath.Join(dir, "speech"), Runtime: dir, Model: "m/x", Voice: "af_heart:0.6,af_bella:0.4"}
}

func TestStatus(t *testing.T) {
	e := testEngine(t)
	if e.Status() != "not installed" {
		t.Errorf("fresh: %q", e.Status())
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(e.Dir, "venv", "bin"), 0o700))
	must(os.WriteFile(e.python(), nil, 0o700))
	write := func(r record) {
		b, _ := json.Marshal(r)
		must(os.WriteFile(e.marker(), b, 0o600))
	}
	write(record{Requirements: "old", Model: e.Model, Voice: e.Voice})
	if !strings.Contains(e.Status(), "older acmd") {
		t.Errorf("old requirements: %q", e.Status())
	}
	write(record{Requirements: requirementsHash(), Model: e.Model, Voice: "af_heart:0.5,af_bella:0.5"})
	if e.Status() != "" {
		t.Errorf("only weights changed (same voices): %q", e.Status())
	}
	write(record{Requirements: requirementsHash(), Model: e.Model, Voice: "af_sky"})
	if !strings.Contains(e.Status(), "another model or voice") {
		t.Errorf("other voice: %q", e.Status())
	}
	other := *e
	other.Voice = "af_sky"
	if e.Socket() == other.Socket() {
		t.Error("the socket doesn't follow the voice")
	}
}

func TestSayStreams(t *testing.T) {
	e := testEngine(t)
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu, 0)
	player := &fakePlayer{}
	orig := NewPlayer
	t.Cleanup(func() { NewPlayer = orig })
	NewPlayer = func(rate int) (pcmPlayer, error) {
		if rate != 24000 {
			t.Errorf("rate %d", rate)
		}
		return player, nil
	}
	md := "First part.\n\n```\ncode\n```\n\nLast part."
	if err := e.Say(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if strings.Join(texts, "|") != "First part.|"+codeNote+"|Last part." || player.bytes != 12 || !player.drained || !player.closed {
		t.Errorf("texts %q player %+v", texts, player)
	}
}

func TestSayStops(t *testing.T) {
	e := testEngine(t)
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu, 30*time.Millisecond)
	player := &fakePlayer{}
	orig := NewPlayer
	t.Cleanup(func() { NewPlayer = orig })
	NewPlayer = func(int) (pcmPlayer, error) { return player, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := e.Say(ctx, strings.Repeat("A sentence goes here. ", 200)); err != nil {
		t.Fatalf("a stopped Say is not an error: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	n := len(texts)
	mu.Unlock()
	if n > 6 || !player.closed {
		t.Errorf("the engine kept going after stop (%d lines) or the player stayed open", n)
	}
}

func TestStartNotInstalled(t *testing.T) {
	e := testEngine(t)
	if err := e.Start(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("start: %v", err)
	}
}
