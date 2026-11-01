//go:build !darwin || !cgo

package speech

import "errors"

func newSystemPlayer(int) (pcmPlayer, error) {
	return nil, errors.New("playing speech needs macOS (built with cgo)")
}
