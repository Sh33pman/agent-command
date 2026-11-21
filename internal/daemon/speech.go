package daemon

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/speech"
)

// Speech is a device op: the UIs on this machine read aloud through their
// local daemon, which runs the speech helper (internal/speech). No UI starts a
// process for it.

// Speaker is the speech engine, for the voice and speed set. Tests swap it.
type Speaker interface {
	// Supported says whether the helper is here at all.
	Supported() bool
	// Status is "" when the model is set up for the voice, else what's missing.
	Status() string
	// HasLegacy says an older engine is set up.
	HasLegacy() bool
	Say(ctx context.Context, text string) error
	Stop(ctx context.Context) error
	// Install downloads the model, telling progress each percent.
	Install(ctx context.Context, progress func(percent int)) error
}

// engineSpeaker is the real engine.
type engineSpeaker struct{ e *speech.Engine }

func (s engineSpeaker) Supported() bool                            { return s.e.Supported() }
func (s engineSpeaker) Status() string                             { return s.e.Status() }
func (s engineSpeaker) HasLegacy() bool                            { return s.e.HasLegacy() }
func (s engineSpeaker) Say(ctx context.Context, text string) error { return s.e.Say(ctx, text) }
func (s engineSpeaker) Stop(ctx context.Context) error             { return s.e.Stop(ctx) }
func (s engineSpeaker) Install(ctx context.Context, progress func(int)) error {
	s.e.Progress = progress
	return s.e.Install(ctx, io.Discard)
}

// speaker is the engine for the settings in effect.
func (d *Daemon) speaker() Speaker {
	c := d.config()
	if d.cfg.Speaker != nil {
		return d.cfg.Speaker(c)
	}
	return engineSpeaker{&speech.Engine{Dir: filepath.Join(d.cfg.Paths.StateDir, "speech"), Runtime: d.cfg.Paths.RuntimeDir,
		Voice: c.Speech.Voice, Speed: c.Speech.Speed}}
}

type speechState struct {
	mu         sync.Mutex
	installing bool
	progress   int // -1 before it's known
	failure    string
	speaking   bool
	stopSay    context.CancelFunc
}

// speechStatus is how speech stands here.
func (d *Daemon) speechStatus() *ipc.SpeechState {
	sp := d.speaker()
	c := d.config()
	st := &ipc.SpeechState{Supported: sp.Supported(), Enabled: c.Speech.Enabled, Voice: c.Speech.Voice}
	if st.Supported {
		status := sp.Status()
		st.Installed = status == ""
		st.Older = !st.Installed && sp.HasLegacy()
	}
	s := &d.speaking
	s.mu.Lock()
	st.Installing, st.Failure, st.Speaking = s.installing, s.failure, s.speaking
	if s.installing && s.progress >= 0 {
		p := s.progress
		st.Progress = &p
	}
	s.mu.Unlock()
	return st
}

// say reads text aloud, and returns when it's done or stopped.
func (d *Daemon) say(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return invalid("nothing to say")
	}
	st := d.speechStatus()
	switch {
	case !st.Supported:
		return invalid("speech isn't available on this machine")
	case !st.Enabled:
		return invalid("speech is off: turn it on in Settings")
	case st.Installing:
		return invalid("speech is still being set up: it'll be ready soon")
	case !st.Installed:
		return invalid("speech isn't set up for this voice yet")
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &d.speaking
	s.mu.Lock()
	if s.stopSay != nil {
		s.stopSay() // one at a time: the new one replaces it
	}
	s.stopSay, s.speaking = cancel, true
	s.mu.Unlock()
	d.changes.bump()
	err := d.speaker().Say(ctx, text)
	s.mu.Lock()
	if ctx.Err() == nil || s.stopSay == nil {
		s.speaking, s.stopSay = false, nil
	}
	s.mu.Unlock()
	cancel()
	d.changes.bump()
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil // stopped
	}
	return err
}

// stopSpeech stops reading aloud, whatever started it.
func (d *Daemon) stopSpeech(ctx context.Context) error {
	s := &d.speaking
	s.mu.Lock()
	if s.stopSay != nil {
		s.stopSay()
		s.stopSay = nil
	}
	s.speaking = false
	s.mu.Unlock()
	d.changes.bump()
	return d.speaker().Stop(ctx)
}

// installSpeech sets speech up for the voice, in the background: the
// snapshot's speech says how far it's got, and why it failed.
func (d *Daemon) installSpeech() *ipc.SpeechState {
	sp := d.speaker()
	s := &d.speaking
	s.mu.Lock()
	if s.installing || !sp.Supported() {
		s.mu.Unlock()
		return d.speechStatus()
	}
	s.installing, s.progress, s.failure = true, -1, ""
	s.mu.Unlock()
	d.changes.bump()
	go func() {
		err := sp.Install(context.Background(), func(p int) {
			s.mu.Lock()
			changed := p != s.progress
			s.progress = p
			s.mu.Unlock()
			if changed {
				d.changes.bump()
			}
		})
		s.mu.Lock()
		s.installing = false
		if err != nil {
			s.failure = err.Error()
		}
		s.mu.Unlock()
		d.changes.bump()
	}()
	return d.speechStatus()
}

func isSpeechOp(op string) bool {
	switch op {
	case ipc.OpSpeechSay, ipc.OpSpeechStop, ipc.OpSpeechStatus, ipc.OpSpeechInstall:
		return true
	}
	return false
}

func (d *Daemon) handleSpeech(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpSpeechSay:
		r, err := decode[ipc.SpeechSay](req.Data)
		if err != nil {
			return nil, err
		}
		return nil, d.say(ctx, r.Text)
	case ipc.OpSpeechStop:
		return nil, d.stopSpeech(ctx)
	case ipc.OpSpeechInstall:
		return d.installSpeech(), nil
	default:
		return d.speechStatus(), nil
	}
}
