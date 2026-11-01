package cli

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFitGateRunsOneAtATime(t *testing.T) {
	old := fitSettle
	fitSettle = 5 * time.Millisecond
	defer func() { fitSettle = old }()

	var g fitGate
	var running, most, runs atomic.Int32
	fit := func(context.Context) error {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		runs.Add(1)
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return nil
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.Run(context.Background(), fit) }()
		time.Sleep(2 * time.Millisecond)
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d fits ran at once", most.Load())
	}
	if r := runs.Load(); r < 1 || r > 3 {
		t.Errorf("%d fits for one burst, want 1-3", r)
	}
}
