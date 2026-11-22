package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// The local hello agrees a protocol version (design 8.2): the highest both
// speak, with the daemon's ops, so a client a version apart still connects
// and uses what both have. None in common is refused, saying which to update.
func TestTheHelloAgreesAVersion(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	hello := func(h ipc.Hello) (ipc.Hello, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5e9)
		defer cancel()
		c, err := ipc.Dial(ctx, p.Socket(), h)
		if c != nil {
			defer c.Close()
			return c.Daemon, err
		}
		return ipc.Hello{}, err
	}
	// A newer client, speaking this version and the next.
	got, err := hello(ipc.Hello{ProtoMin: ipc.ProtoMin, ProtoMax: ipc.Proto + 1, Version: testVersion})
	if err != nil || got.Proto != ipc.Proto || got.ProtoMin != ipc.ProtoMin || got.ProtoMax != ipc.Proto {
		t.Fatalf("a client a version ahead: %+v %v", got, err)
	}
	if len(got.Ops) == 0 || !strings.Contains(strings.Join(got.Ops, " "), ipc.OpSnapshot) {
		t.Errorf("ops = %v", got.Ops)
	}
	// One from before ranges: only proto.
	if got, err := hello(ipc.Hello{Proto: ipc.Proto, Version: testVersion}); err != nil || got.Proto != ipc.Proto {
		t.Errorf("an older client: %+v %v", got, err)
	}
	// Only newer versions: update acmd.
	_, err = hello(ipc.Hello{ProtoMin: ipc.Proto + 1, ProtoMax: ipc.Proto + 2, Version: testVersion})
	if codeOf(err) != errcode.ProtoUnsupported || !strings.Contains(err.Error(), "update acmd") {
		t.Errorf("a client too new: %v", err)
	}
	// Only older ones than this daemon speaks: update the client.
	if ipc.ProtoMin > 1 {
		_, err = hello(ipc.Hello{ProtoMin: 1, ProtoMax: ipc.ProtoMin - 1, Version: testVersion})
		if codeOf(err) != errcode.ProtoUnsupported || !strings.Contains(err.Error(), "update this client") {
			t.Errorf("a client too old: %v", err)
		}
	}
	// A daemon of another build is still a mismatch, for the CLI to replace.
	var mismatch *ipc.ErrVersionMismatch
	if _, err := hello(ipc.HelloFrom("other")); !errors.As(err, &mismatch) {
		t.Errorf("another build: %v", err)
	}
	// And the reply as the clients get it.
	c := dial(t, p)
	if !c.Has(ipc.OpSnapshot) || c.Has("no.such.op") {
		t.Error("Has")
	}
	if _, err := (&Daemon{}).helloReply([]byte("nope")); resultOf("", err).Code != errcode.BadRequest {
		t.Errorf("a hello that isn't one: %v", err)
	}
}
