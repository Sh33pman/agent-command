package cli

import (
	"context"
	"sync"
	"time"
)

// fitSettle is how long a fit waits for a burst of resize events to end.
var fitSettle = 150 * time.Millisecond

// fitGate runs one width fit at a time. Each resize of the sidebar sends it
// a new size event, and fits that ran side by side read stale widths and
// pushed the split back and forth without end. A call that arrives while a
// fit runs only asks for one more fit afterwards.
type fitGate struct {
	mu      sync.Mutex
	running bool
	again   bool
}

// Run waits fitSettle, then runs fit, repeating once if asked meanwhile.
func (g *fitGate) Run(ctx context.Context, fit func(context.Context) error) error {
	g.mu.Lock()
	if g.running {
		g.again = true
		g.mu.Unlock()
		return nil
	}
	g.running = true
	g.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			g.finish()
			return ctx.Err()
		case <-time.After(fitSettle):
		}
		g.mu.Lock()
		g.again = false
		g.mu.Unlock()
		err := fit(ctx)
		g.mu.Lock()
		if err != nil || !g.again {
			// Checked and cleared together, so a request can't slip
			// between them and be lost.
			g.running, g.again = false, false
			g.mu.Unlock()
			return err
		}
		g.mu.Unlock()
	}
}

func (g *fitGate) finish() {
	g.mu.Lock()
	g.running, g.again = false, false
	g.mu.Unlock()
}
