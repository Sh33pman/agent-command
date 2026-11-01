//go:build darwin && cgo

package speech

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation
#include <AudioToolbox/AudioToolbox.h>
#include <string.h>
#include <stdlib.h>

// acmdCount tracks a queue's buffers: queued and played (the callback
// runs on AudioQueue's own thread).
typedef struct { volatile long queued, played; } acmdCount;

static void acmdDone(void *data, AudioQueueRef q, AudioQueueBufferRef b) {
	__sync_fetch_and_add(&((acmdCount *)data)->played, 1);
	AudioQueueFreeBuffer(q, b);
}

// acmdQueueNew makes an output queue for 16-bit mono PCM at rate Hz.
static OSStatus acmdQueueNew(double rate, acmdCount *c, AudioQueueRef *q) {
	AudioStreamBasicDescription f;
	memset(&f, 0, sizeof f);
	f.mSampleRate = rate;
	f.mFormatID = kAudioFormatLinearPCM;
	f.mFormatFlags = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
	f.mChannelsPerFrame = 1;
	f.mBitsPerChannel = 16;
	f.mBytesPerFrame = 2;
	f.mFramesPerPacket = 1;
	f.mBytesPerPacket = 2;
	return AudioQueueNewOutput(&f, acmdDone, c, NULL, NULL, 0, q);
}

static OSStatus acmdQueueEnqueue(AudioQueueRef q, acmdCount *c, const void *data, UInt32 n) {
	AudioQueueBufferRef b;
	OSStatus st = AudioQueueAllocateBuffer(q, n, &b);
	if (st != noErr) return st;
	memcpy(b->mAudioData, data, n);
	b->mAudioDataByteSize = n;
	__sync_fetch_and_add(&c->queued, 1);
	st = AudioQueueEnqueueBuffer(q, b, 0, NULL);
	if (st != noErr) __sync_fetch_and_sub(&c->queued, 1);
	return st;
}

static int acmdQueuePending(acmdCount *c) {
	return __sync_fetch_and_add(&c->played, 0) < __sync_fetch_and_add(&c->queued, 0);
}
*/
import "C"

import (
	"context"
	"fmt"
	"time"
	"unsafe"
)

// queuePlayer plays PCM through an AudioQueue as it arrives (no files).
type queuePlayer struct {
	q       C.AudioQueueRef
	count   *C.acmdCount // C memory: AudioQueue's thread writes it
	started bool
}

func newSystemPlayer(rate int) (pcmPlayer, error) {
	p := &queuePlayer{count: (*C.acmdCount)(C.calloc(1, C.sizeof_acmdCount))}
	st := C.acmdQueueNew(C.double(rate), p.count, &p.q) //nolint:gocritic // dupSubExpr misreads cgo's generated call
	if int(st) != 0 {
		C.free(unsafe.Pointer(p.count))
		return nil, fmt.Errorf("audio output: OSStatus %d", int(st))
	}
	return p, nil
}

func (p *queuePlayer) Write(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	if st := C.acmdQueueEnqueue(p.q, p.count, unsafe.Pointer(&pcm[0]), C.UInt32(len(pcm))); st != 0 {
		return fmt.Errorf("audio output: OSStatus %d", int(st))
	}
	if !p.started {
		if st := C.AudioQueueStart(p.q, nil); st != 0 {
			return fmt.Errorf("audio output: OSStatus %d", int(st))
		}
		p.started = true
	}
	return nil
}

// drainTail covers the audio still in the output hardware when the queue
// reports its last buffer played.
const drainTail = 250 * time.Millisecond

// Drain waits until everything written has played (or ctx ends).
func (p *queuePlayer) Drain(ctx context.Context) error {
	if !p.started {
		return nil
	}
	C.AudioQueueFlush(p.q)
	for C.acmdQueuePending(p.count) != 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(drainTail):
	}
	return nil
}

// Close stops at once and frees the queue.
func (p *queuePlayer) Close() {
	C.AudioQueueStop(p.q, C.Boolean(1))
	C.AudioQueueDispose(p.q, C.Boolean(1))
	C.free(unsafe.Pointer(p.count))
}
