package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

// Terminal clients: acmd attach, acmd watch and the terminal UI. They tell the
// daemon who they are, as the apps do, so notice routing (notify.route) can
// pick the machine you're typing on. They share one id per machine, kept in
// acmd's state folder: a notice routed to it shows in every one that's open.

// terminalClientFile holds the id.
func terminalClientFile(p paths.Paths) string { return filepath.Join(p.StateDir, "terminal-client.id") }

// terminalClientID is this machine's terminal client id, made the first time.
func terminalClientID(p paths.Paths) (string, error) {
	if b, err := os.ReadFile(terminalClientFile(p)); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := p.Ensure(); err != nil {
		return "", err
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "terminal-" + hex.EncodeToString(b)
	if err := os.WriteFile(terminalClientFile(p), []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// terminalHello is a terminal client's hello: it shows notices, and attaches
// to sessions, on this machine.
func terminalHello(p paths.Paths) (ipc.Hello, error) {
	id, err := terminalClientID(p)
	if err != nil {
		return ipc.Hello{}, err
	}
	device, _ := os.Hostname()
	h := ipc.HelloFrom(version.Get().Version)
	h.Client = &ipc.ClientHello{ID: id, Kind: ipc.KindTerminal, Device: device,
		Caps: []string{ipc.CapNotify, ipc.CapAttach}, OnHost: true}
	return h, nil
}

// connectTerminal connects as this machine's terminal client, in use now: its
// connection stays open while the command runs, which is what keeps it
// counted as there.
func connectTerminal(ctx context.Context) (*client, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	h, err := terminalHello(p)
	if err != nil {
		return nil, err
	}
	c, err := connectAs(ctx, h)
	if err != nil {
		return nil, err
	}
	c.terminal = h.Client.ID
	if err := c.conn.Call(ipc.OpClientPresence, ipc.ClientPresence{Presence: ipc.PresenceActive}, nil); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}
