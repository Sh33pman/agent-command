package cli

import (
	"context"
	"time"
)

// pollResize reads size every interval and tells when it changed, until ctx
// ends. It's how a terminal without SIGWINCH (Windows) follows its size.
func pollResize(ctx context.Context, size func() (int, int), every time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		defer close(out)
		r, c := size()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if nr, nc := size(); nr != r || nc != c {
					r, c = nr, nc
					select {
					case out <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return out
}
