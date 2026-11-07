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
	"sync/atomic"
	"testing"
	"time"
)

// fakeEngine answers like acmd-speech: speak records each line of text and takes
// slow per line unless it's stopped, and stop ends it.
func fakeEngine(t *testing.T, sock string, texts *[]string, mu *sync.Mutex, slow time.Duration) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	stop := make(chan struct{}, 1)
	var speaking atomic.Bool
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
				case "stop":
					stop <- struct{}{}
				case "status":
					reply["speaking"] = speaking.Load()
				case "stream":
					_, _ = c.Write([]byte(`{"ok":true,"rate":24000}` + "\n"))
					for _, l := range strings.Split(req["text"].(string), "\n") {
						mu.Lock()
						*texts = append(*texts, l)
						mu.Unlock()
						if _, err := c.Write([]byte{0, 0, 0, 4, 1, 2, 3, 4}); err != nil {
							return
						}
					}
					_, _ = c.Write([]byte{0, 0, 0, 0})
					return
				case "speak":
					_, _ = c.Write([]byte(`{"ok":true}` + "\n"))
					speaking.Store(true)
					defer speaking.Store(false)
					for _, l := range strings.Split(req["text"].(string), "\n") {
						mu.Lock()
						*texts = append(*texts, l)
						mu.Unlock()
						select {
						case <-stop:
							_, _ = c.Write([]byte(`{"done":true}` + "\n"))
							return
						case <-time.After(slow):
						}
					}
					_, _ = c.Write([]byte(`{"done":true}` + "\n"))
					return
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
	e := &Engine{Dir: filepath.Join(dir, "speech"), Runtime: dir, Voice: "af_heart:0.6,af_bella:0.4", Helper: "/bin/true"}
	// Already downloaded, so a test's install doesn't go looking for the model.
	if err := os.MkdirAll(e.modelUnpacked(), 0o700); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSayAsksTheEngineToSpeak(t *testing.T) {
	e := testEngine(t)
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu, 0)
	md := "First part.\n\n```\ncode\n```\n\nLast part."
	if err := e.Say(context.Background(), md); err != nil {
		t.Fatal(err)
	}
	if strings.Join(texts, "|") != "First part.|"+codeNote+"|Last part." {
		t.Errorf("texts %q", texts)
	}
}

func TestEndingSayStopsTheEngine(t *testing.T) {
	e := testEngine(t)
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu, 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := e.Say(ctx, strings.Repeat("A sentence goes here. ", 200)); err != nil {
		t.Fatalf("a stopped Say is not an error: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	n := len(texts)
	mu.Unlock()
	if n > 6 {
		t.Errorf("the engine kept going after stop (%d lines)", n)
	}
}

func TestStopAndStatusFromAnotherClient(t *testing.T) {
	e := testEngine(t)
	if running, _ := e.EngineStatus(context.Background()); running {
		t.Error("running with no engine")
	}
	if err := e.Stop(context.Background()); err != nil {
		t.Errorf("stop with no engine: %v", err)
	}
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu, 20*time.Second)
	said := make(chan error, 1)
	go func() { said <- e.Say(context.Background(), "A long sentence goes here.") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if running, speaking := e.EngineStatus(context.Background()); running && speaking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never started speaking")
		}
		time.Sleep(5 * time.Millisecond)
	}
	must(t, e.Stop(context.Background()))
	select {
	case err := <-said:
		if err != nil {
			t.Errorf("say: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("say didn't end when another client stopped it")
	}
}

func TestStartNotInstalled(t *testing.T) {
	e := testEngine(t)
	if err := e.Start(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("start: %v", err)
	}
}
