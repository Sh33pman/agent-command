package termtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/term"
)

type buf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *buf) Write(p []byte) (int, error) { b.mu.Lock(); defer b.mu.Unlock(); return b.b.Write(p) }
func (b *buf) String() string              { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func TestEchoShowsWhatsTyped(t *testing.T) {
	dir, err := os.MkdirTemp("", "tt") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	Echo(t, sock)
	if !term.Running(sock) {
		t.Fatal("not running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan []byte, 1)
	out := &buf{}
	done := make(chan error, 1)
	go func() { done <- term.Attach(ctx, sock, in, nil, 24, 80, out) }()
	in <- []byte("hello\n")
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), "hello"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("output %q", out.String())
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Logf("attach ended: %v", err)
	}
}
