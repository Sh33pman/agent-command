package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// acmd events prints the stream, one JSON event per line, until stopped.
func TestEventsCommand(t *testing.T) {
	env(t)
	r, w := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		root := NewRootCmd("acmd", w, &bytes.Buffer{})
		root.SetArgs([]string{"events", "--types", "session"})
		done <- root.ExecuteContext(ctx)
		w.Close()
	}()
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 1<<20), 1<<20)
	next := func() ipc.Event {
		t.Helper()
		if !lines.Scan() {
			t.Fatal("the stream ended")
		}
		var ev ipc.Event
		must(t, json.Unmarshal(lines.Bytes(), &ev))
		return ev
	}
	snap := next()
	if snap.Type != ipc.EventSnapshot || len(snap.Snapshot.Sessions) == 0 || snap.Snapshot.Groups != nil {
		t.Fatalf("snapshot = %+v", snap)
	}
	mustRun(t, "rename", snap.Snapshot.Sessions[0].ID, "--to", "renamed")
	for {
		ev := next()
		if ev.Type == ipc.EventUpdated && ev.Entity == ipc.EntitySession && ev.ID == snap.Snapshot.Sessions[0].ID {
			break
		}
	}
	cancel()
	go func() { _, _ = io.Copy(io.Discard, r) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stopped: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("didn't stop")
	}
	if _, err := acmd(t, "events", "--types", "weather"); ExitCode(err) != exitUsage {
		t.Errorf("a type that isn't one: %v", err)
	}
}
