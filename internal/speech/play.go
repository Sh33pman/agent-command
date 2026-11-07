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
	"os"
	"strings"
	"syscall"
	"time"
)

// endOfError marks a stream that failed after it started.
const endOfError = 0xFFFFFFFF

// Say reads markdown aloud (code blocks and tables become a short note, Prepare)
// and returns when it's done, when something replaces it, or when ctx ends, which
// stops it. The engine does the playing, so anything else can stop it too.
func (e *Engine) Say(ctx context.Context, markdown string) error {
	chunks := Prepare(markdown, chunkSize)
	if len(chunks) == 0 {
		return nil
	}
	if err := e.Start(ctx); err != nil {
		return err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", e.Socket())
	if err != nil {
		return err
	}
	defer c.Close()
	// One line per chunk: the engine speaks each line in turn.
	req, _ := json.Marshal(map[string]any{"op": "speak", "text": strings.Join(chunks, "\n"), "speed": e.speed()})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() {
		// The context has ended, so stopping gets one of its own.
		quick, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = e.Stop(quick)
		_ = c.Close()
	})
	defer stop()
	r := bufio.NewReader(c)
	var reply struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		return stopped(ctx, err)
	}
	if err := json.Unmarshal(line, &reply); err != nil || !reply.OK {
		return fmt.Errorf("speech engine: %s", reply.Error)
	}
	line, err = r.ReadBytes('\n')
	if err != nil {
		return stopped(ctx, fmt.Errorf("the speech engine stopped (see %s): %w", e.logFile(), err))
	}
	reply.Error = ""
	if err := json.Unmarshal(line, &reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("speech engine: %s", reply.Error)
	}
	return nil
}

// Stop stops whatever the engine is playing, if it's running at all.
func (e *Engine) Stop(ctx context.Context) error {
	_, err := e.request(ctx, map[string]any{"op": "stop"})
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	return err
}

// EngineStatus says whether the engine is running, and whether it's playing.
func (e *Engine) EngineStatus(ctx context.Context) (running, speaking bool) {
	reply, err := e.request(ctx, map[string]any{"op": "status"})
	if err != nil {
		return false, false
	}
	speaking, _ = reply["speaking"].(bool)
	return true, speaking
}

func (e *Engine) speed() float64 {
	if e.Speed <= 0 {
		return 1
	}
	return e.Speed
}

// stream sends chunks to the running engine as one stream request: start
// gets the sample rate before any audio, then pcm each frame as it comes.
// Ending ctx closes the connection, which stops the engine too.
func (e *Engine) stream(ctx context.Context, chunks []string, start func(rate int) error, pcm func([]byte) error) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", e.Socket())
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() }) // stops the engine too
	defer stop()
	// One line per chunk: the engine speaks each line in turn.
	req, _ := json.Marshal(map[string]any{"op": "stream", "text": strings.Join(chunks, "\n"), "speed": e.speed()})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return err
	}
	r := bufio.NewReaderSize(c, 64<<10)
	head, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	var hdr struct {
		OK    bool   `json:"ok"`
		Rate  int    `json:"rate"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(head, &hdr); err != nil || !hdr.OK {
		return fmt.Errorf("speech engine: %s", hdr.Error)
	}
	if err := start(hdr.Rate); err != nil {
		return err
	}
	var n [4]byte
	for {
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return err
		}
		size := binary.BigEndian.Uint32(n[:])
		switch size {
		case 0:
			return nil
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
		b := make([]byte, size)
		if _, err := io.ReadFull(r, b); err != nil {
			return err
		}
		if err := pcm(b); err != nil {
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

// chunkSize is the most characters per line sent to the engine: the
// engine streams each line as soon as it's spoken.
const chunkSize = 350
