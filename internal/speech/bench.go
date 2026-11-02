package speech

import (
	"context"
	"time"
)

// BenchResult is how fast an engine spoke one text (acmd speech bench).
type BenchResult struct {
	Started    bool          // the engine had to be started (loaded)
	Ready      time.Duration // until it answered (0 when already running)
	FirstAudio time.Duration // from the request to the first audio
	Total      time.Duration // from the request to the last audio
	Audio      time.Duration // how long the audio plays
}

// RTF is the real-time factor: generation time per second of audio.
func (r BenchResult) RTF() float64 {
	if r.Audio <= 0 {
		return 0
	}
	return r.Total.Seconds() / r.Audio.Seconds()
}

// Bench speaks markdown without playing it and times it.
func (e *Engine) Bench(ctx context.Context, markdown string) (BenchResult, error) {
	var r BenchResult
	t0 := time.Now()
	if _, err := e.request(ctx, map[string]any{"op": "ping"}); err != nil {
		r.Started = true
		if err := e.Start(ctx); err != nil {
			return r, err
		}
		r.Ready = time.Since(t0)
	}
	var rate, bytes int
	t1 := time.Now()
	err := e.stream(ctx, Prepare(markdown, chunkSize), func(hz int) error { rate = hz; return nil },
		func(pcm []byte) error {
			if bytes == 0 {
				r.FirstAudio = time.Since(t1)
			}
			bytes += len(pcm)
			return nil
		})
	r.Total = time.Since(t1)
	if rate > 0 {
		r.Audio = time.Duration(float64(bytes/2) / float64(rate) * float64(time.Second))
	}
	return r, stopped(ctx, err)
}
