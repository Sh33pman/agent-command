package term

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// Attach shows the terminal on socket: the kept output, then live output
// to out; input from in (a channel, so one stdin reader serves every
// attach) and sizes from size go to the shell. It returns nil when ctx
// ends (detached) and ErrEnded when the shell is gone.
func Attach(ctx context.Context, socket string, in <-chan []byte, size <-chan [2]int, rows, cols int, out io.Writer) error {
	c, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return ErrEnded // no server: the terminal ended
	}
	defer c.Close()
	if err := writeFrame(c, frameAttach, nil); err != nil {
		return ErrEnded
	}
	if err := writeFrame(c, frameResize, resizePayload(rows, cols)); err != nil {
		return ErrEnded
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(out, c)
		done <- err
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			if ctx.Err() != nil {
				return nil
			}
			_ = err // the connection closed: the shell is gone
			return ErrEnded
		case b, ok := <-in:
			if !ok {
				return errors.New("term: input closed")
			}
			if err := writeFrame(c, frameInput, b); err != nil {
				return ErrEnded
			}
		case sz := <-size:
			if err := writeFrame(c, frameResize, resizePayload(sz[0], sz[1])); err != nil {
				return ErrEnded
			}
		}
	}
}
