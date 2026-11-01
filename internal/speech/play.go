package speech

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// pcmPlayer plays 16-bit mono PCM as it's written.
type pcmPlayer interface {
	Write(pcm []byte) error
	// Drain waits until everything written has played.
	Drain(ctx context.Context) error
	// Close stops at once.
	Close()
}

// NewPlayer makes the player for audio at rate Hz (swappable in tests):
// an AudioQueue on macOS, playing from memory as audio arrives.
var NewPlayer = newSystemPlayer

// endOfError marks a stream that failed after it started.
const endOfError = 0xFFFFFFFF

// Say reads markdown aloud (code blocks and tables become a short note,
// Prepare) and returns when it's done or ctx ends (stop). It streams: the
// engine sends audio as Kokoro makes each piece and it plays from memory
// at once; nothing is written to disk.
func (e *Engine) Say(ctx context.Context, markdown string) error {
	chunks := Prepare(markdown, chunkSize)
	if len(chunks) == 0 {
		return nil
	}
	if err := e.Start(ctx); err != nil {
		return err
	}
	speed := e.Speed
	if speed <= 0 {
		speed = 1
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", e.Socket())
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() }) // stops the engine too
	defer stop()
	// One line per chunk: Kokoro splits on newlines and streams each.
	req, _ := json.Marshal(map[string]any{"op": "stream", "text": strings.Join(chunks, "\n"), "speed": speed})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return err
	}
	r := bufio.NewReaderSize(c, 64<<10)
	head, err := r.ReadBytes('\n')
	if err != nil {
		return stopped(ctx, err)
	}
	var hdr struct {
		OK    bool   `json:"ok"`
		Rate  int    `json:"rate"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(head, &hdr); err != nil || !hdr.OK {
		return fmt.Errorf("speech engine: %s", hdr.Error)
	}
	p, err := NewPlayer(hdr.Rate)
	if err != nil {
		return err
	}
	defer p.Close()
	var n [4]byte
	for {
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return stopped(ctx, err)
		}
		size := binary.BigEndian.Uint32(n[:])
		switch size {
		case 0:
			return stopped(ctx, p.Drain(ctx))
		case endOfError:
			line, _ := r.ReadBytes('\n')
			var e struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(line, &e)
			return fmt.Errorf("speech engine: %s", e.Error)
		}
		if size > 64<<20 {
			return errors.New("speech engine: bad audio frame")
		}
		pcm := make([]byte, size)
		if _, err := io.ReadFull(r, pcm); err != nil {
			return stopped(ctx, err)
		}
		if err := p.Write(pcm); err != nil {
			return err
		}
	}
}

// stopped is nil when ctx ended (the user stopped playback), else err.
func stopped(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// chunkSize is the most characters per line sent to the engine: Kokoro
// streams each line as soon as it's spoken.
const chunkSize = 350
