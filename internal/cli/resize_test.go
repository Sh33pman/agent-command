package cli

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollResizeTellsOnlyWhenTheSizeChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var cols atomic.Int32
	cols.Store(80)
	got := pollResize(ctx, func() (int, int) { return 24, int(cols.Load()) }, 5*time.Millisecond)

	select {
	case <-got:
		t.Fatal("told of a change while the size stayed the same")
	case <-time.After(50 * time.Millisecond):
	}
	cols.Store(120)
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("not told the size changed")
	}
	cancel()
	for range got { // closed once ctx ends
	}
}
