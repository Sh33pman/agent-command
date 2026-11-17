package term

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// newTestState is a server's shared state with no program behind it.
func newTestState() *serverState {
	return &serverState{clients: map[net.Conn]*client{}, screen: &ringScreen{kept: ring{n: 1 << 16}}}
}

// A pane that stops reading must not hold up the program's output, the lock,
// or the other panes. It is dropped once it falls too far behind.
func TestAStalledPaneDoesNotHoldUpTheOthers(t *testing.T) {
	st := newTestState()

	stalled, stalledFar := net.Pipe() // nobody ever reads stalledFar
	defer stalledFar.Close()
	st.attach(attachRequest{conn: stalled})

	healthy, healthyFar := net.Pipe()
	got := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(healthyFar); got <- b }()
	st.attach(attachRequest{conn: healthy})

	want := bytes.NewBuffer(st.screen.Replay())
	start := time.Now()
	for i := 0; i < clientQueue*2; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, 100)
		want.Write(chunk)
		st.output(chunk)
	}
	// The stalled pane's queue fills, the program waits clientWriteTimeout for
	// it once, and then it is dropped.
	if took := time.Since(start); took > clientWriteTimeout+time.Second {
		t.Errorf("output took %v with a stalled pane attached", took)
	}

	st.mu.Lock()
	_, stillThere := st.clients[stalled]
	st.mu.Unlock()
	if stillThere {
		t.Error("the stalled pane wasn't dropped")
	}

	st.close()
	select {
	case b := <-got:
		if !bytes.Equal(b, want.Bytes()) {
			t.Errorf("the healthy pane got %d bytes, want %d, in order", len(b), want.Len())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the healthy pane never got its output")
	}
}

// Attaching sends the screen first, then what the program writes after.
func TestAttachSendsTheScreenThenNewOutput(t *testing.T) {
	st := newTestState()
	st.output([]byte("before "))
	replay := string(st.screen.Replay())
	c, far := net.Pipe()
	got := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(far); got <- b }()
	st.attach(attachRequest{conn: c})
	st.output([]byte("after"))
	st.close()
	if b := <-got; string(b) != replay+"after" {
		t.Errorf("got %q, want the screen, then %q", b, "after")
	}
}

// While a pane is stalled, the lock is free: the host still answers what is
// on screen and what runs in front.
func TestTheLockIsFreeWhileAPaneIsStalled(t *testing.T) {
	st := newTestState()
	stalled, stalledFar := net.Pipe()
	defer stalledFar.Close()
	st.attach(attachRequest{conn: stalled})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < clientQueue*2; i++ {
			st.output([]byte("x"))
		}
	}()
	time.Sleep(100 * time.Millisecond) // the queue is full and output is waiting
	start := time.Now()
	_ = st.text()
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("reading the screen waited %v for a stalled pane", took)
	}
	<-done
	st.close()
}
