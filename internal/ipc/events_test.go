package ipc

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Subscribe turns a connection into the stream, read with NextEvent.
func TestTheStreamOnAConnection(t *testing.T) {
	dir, err := os.MkdirTemp("", "ipcev")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		var req Request
		for range 2 {
			if ReadJSON(r, &req) != nil {
				return
			}
			switch req.Op {
			case OpHello:
				_ = WriteJSON(c, Response{OK: true, Data: []byte(`{"proto":1,"version":"t"}`)})
			case OpSubscribe:
				_ = WriteJSON(c, Response{OK: true})
				_ = WriteJSON(c, Event{Rev: 7, Type: EventSnapshot, Snapshot: &Snapshot{}})
				_ = WriteJSON(c, Event{Rev: 8, Type: EventRemoved, Entity: EntitySession, ID: "a"})
			}
		}
		_, _ = r.ReadByte() // until the client goes
	}()
	c, err := Dial(context.Background(), sock, Hello{Proto: 1, Version: "t"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(SubscribeRequest{Types: []string{EntitySession}, Since: 3}); err != nil {
		t.Fatal(err)
	}
	if ev, err := c.NextEvent(); err != nil || ev.Type != EventSnapshot || ev.Rev != 7 {
		t.Errorf("first = %+v %v", ev, err)
	}
	if ev, err := c.NextEvent(); err != nil || ev.Type != EventRemoved || ev.ID != "a" {
		t.Errorf("second = %+v %v", ev, err)
	}
}
