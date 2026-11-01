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

// fakeEngine answers like engine.py: ping, and synth (writes a tiny file).
func fakeEngine(t *testing.T, sock string, texts *[]string, mu *sync.Mutex) {
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
				if req["op"] == "synth" {
					mu.Lock()
					*texts = append(*texts, req["text"].(string))
					mu.Unlock()
					_ = os.WriteFile(req["out"].(string), []byte("RIFF"), 0o600)
					reply["seconds"] = 1.5
				}
				b, _ := json.Marshal(reply)
				_, _ = c.Write(append(b, '\n'))
			}()
		}
	}()
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

func TestSayPipelinesAndStops(t *testing.T) {
	e := testEngine(t)
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu)

	var played []string
	orig := PlayFile
	t.Cleanup(func() { PlayFile = orig })
	PlayFile = func(_ context.Context, path string) error {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("playing a missing file: %v", err)
		}
		played = append(played, filepath.Base(path))
		return nil
	}
	md := "First part.\n\n```\ncode\n```\n\nLast part."
	if err := e.Say(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if strings.Join(texts, "|") != "First part.|"+codeNote+"|Last part." || len(played) != 3 {
		t.Errorf("texts %q played %v", texts, played)
	}

	// Stopping mid-way: nothing more is played.
	texts, played = nil, nil
	ctx, cancel := context.WithCancel(context.Background())
	PlayFile = func(ctx context.Context, path string) error {
		played = append(played, path)
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	if err := e.Say(ctx, strings.Repeat("A sentence. ", 200)); err != nil {
		t.Fatal(err)
	}
	if len(played) != 1 {
		t.Errorf("played %d chunks after stop", len(played))
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := len(texts)
	mu.Unlock()
	if n > 3 {
		t.Errorf("kept synthesizing after stop: %d chunks", n)
	}
}

func TestStartNotInstalled(t *testing.T) {
	e := testEngine(t)
	if err := e.Start(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("start: %v", err)
	}
}
