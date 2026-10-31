package reconcile

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// InterruptMarker is what Claude writes to the transcript when the user
// presses Esc; no Stop hook fires in that case (M0 A12, SPEC §5.4).
var InterruptMarker = []byte("[Request interrupted by user")

// maxTailRead bounds how much new transcript is read per check.
const maxTailRead = 1 << 20

// headLen is how much of a transcript's start identifies it: if those bytes
// change, the file was rewritten rather than appended to.
const headLen = 64

// Tailer watches transcripts of busy sessions for the interrupt marker. It
// polls by size, which is cheap and needs no file-watching dependency; it
// only reads what was appended since the last check.
type Tailer struct {
	offsets map[string]tailState
}

type tailState struct {
	size int64
	head string
}

// NewTailer returns an empty Tailer.
func NewTailer() *Tailer { return &Tailer{offsets: map[string]tailState{}} }

// Interrupted reports whether text appended to path since the previous call
// contains the interrupt marker. The first call for a path only records its
// size (history is never rescanned).
func (t *Tailer) Interrupted(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := st.Size()
	head := make([]byte, min(size, headLen))
	if _, err := f.ReadAt(head, 0); err != nil && err != io.EOF {
		return false, err
	}
	prev, seen := t.offsets[path]
	t.offsets[path] = tailState{size: size, head: string(head)}
	if !seen || size <= prev.size || !samePrefix(prev.head, string(head)) {
		// New path, nothing new, or the file was truncated/rewritten.
		return false, nil
	}
	start := prev.size
	if size-start > maxTailRead {
		start = size - maxTailRead
	}
	// Re-read a few bytes before the old offset so a marker split across
	// two checks is still found.
	if back := int64(len(InterruptMarker)); start-back > 0 {
		start -= back
	} else {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return false, err
	}
	return bytes.Contains(buf, InterruptMarker), nil
}

// samePrefix reports whether the previously seen start of the file is still
// there (the file grew by appending).
func samePrefix(prev, now string) bool {
	return len(now) >= len(prev) && now[:len(prev)] == prev
}

// Forget drops a path (session no longer busy).
func (t *Tailer) Forget(path string) { delete(t.offsets, path) }
