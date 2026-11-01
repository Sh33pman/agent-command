package speech

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// chunkSize is the most characters synthesized at once: short enough that
// speech starts quickly, long enough to sound natural.
const chunkSize = 350

// PlayFile plays a WAV file until it ends or ctx ends (swappable in tests).
var PlayFile = func(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "/usr/bin/afplay", path).Run() // #nosec G204 -- our own temp file
}

// Say reads markdown aloud (code blocks and tables become a short note,
// Prepare) and returns when it's done or ctx ends (stop). Each chunk is
// synthesized while the one before it plays.
func (e *Engine) Say(ctx context.Context, markdown string) error {
	chunks := Prepare(markdown, chunkSize)
	if len(chunks) == 0 {
		return nil
	}
	if err := e.Start(ctx); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(e.Runtime, "say-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type made struct {
		path string
		err  error
	}
	ready := make(chan made, 1) // one chunk ahead
	go func() {
		defer close(ready)
		for i, c := range chunks {
			out := filepath.Join(tmp, fmt.Sprintf("%03d.wav", i))
			_, err := e.Synth(ctx, c, out)
			select {
			case ready <- made{out, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for m := range ready {
		if m.err != nil {
			return m.err
		}
		if err := PlayFile(ctx, m.path); err != nil && ctx.Err() == nil {
			return fmt.Errorf("play: %w", err)
		}
		_ = os.Remove(m.path)
		if ctx.Err() != nil {
			return nil // stopped
		}
	}
	return nil
}
