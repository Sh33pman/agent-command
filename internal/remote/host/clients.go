package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Limits on requests from unknown clients.
const (
	// RequestTTL is how long a request is kept after its client last tried.
	RequestTTL = 24 * time.Hour
	// MaxRequests is how many requests are kept at once.
	MaxRequests = 50
	// MaxNewRequests is how many new requests are taken in RequestWindow; more
	// are refused without being kept.
	MaxNewRequests = 10
	RequestWindow  = time.Minute
)

// ErrNoConfig is a change to config.toml with no config.toml to change.
var ErrNoConfig = errors.New("hosting has no config.toml to change")

// ErrAlreadyAuthorized is a client that's already on the list.
var ErrAlreadyAuthorized = errors.New("already authorized")

var errTooManyRequests = errors.New("too many requests from unknown clients: try again later")

// authorize decides on a client after its hello: in if it's on the list,
// otherwise refused, with its request kept for the user.
func (h *Host) authorize(p wire.Peer) wire.Verdict {
	h.mu.Lock()
	if _, ok := h.authorizedLocked(p.Fingerprint); ok {
		h.mu.Unlock()
		return wire.Verdict{Status: wire.StatusOK}
	}
	req, fresh, err := h.recordLocked(p)
	h.mu.Unlock()
	if err != nil {
		h.o.Log.Printf("remote: refused unknown client %s (%q) at %s: %v", p.Fingerprint, p.CommonName, p.Addr, err)
		return wire.Verdict{Status: wire.StatusUnauthorized, Message: err.Error()}
	}
	if fresh {
		h.o.Log.Printf("remote: unknown client %s (%q) at %s: kept as request %s, for you to approve or deny", p.Fingerprint, p.CommonName, p.Addr, req.ID)
	} else {
		h.o.Log.Printf("remote: unknown client %s (%q) at %s: request %s again (%d tries)", p.Fingerprint, p.CommonName, p.Addr, req.ID, req.Count)
	}
	h.changed()
	return wire.Verdict{Status: wire.StatusPending,
		Message: fmt.Sprintf("request %s: approve it on the host, with acmd remote requests approve %s", req.ID, req.ID)}
}

// authorizedLocked finds a client on the list by its fingerprint (h.mu is
// held).
func (h *Host) authorizedLocked(fp string) (config.AuthorizedClient, bool) {
	for _, ac := range h.remote.AuthorizedClients {
		if identity.Equal(ac.Fingerprint, fp) {
			return ac, true
		}
	}
	return config.AuthorizedClient{}, false
}

// closeUnauthorizedLocked drops the connections of clients no longer on the
// list (h.mu is held).
func (h *Host) closeUnauthorizedLocked() int {
	n := 0
	for c, cn := range h.conns {
		ac, ok := h.authorizedLocked(c.Peer.Fingerprint)
		if !ok {
			_ = c.Close()
			n++
			continue
		}
		cn.name = ac.Name // a rename in config.toml
	}
	return n
}

// recordLocked keeps p's request: a new one, or the one from its fingerprint
// counted again (h.mu is held).
func (h *Host) recordLocked(p wire.Peer) (req ipc.RemoteRequest, fresh bool, err error) {
	now := h.o.Now()
	h.pruneLocked(now)
	for _, r := range h.st.Requests {
		if identity.Equal(r.Fingerprint, p.Fingerprint) {
			// Repeats are saved at most once a minute: a client that keeps
			// trying shouldn't keep writing to disk.
			save := now.Sub(r.LastSeen) >= time.Minute
			r.Count++
			r.LastSeen, r.Addr, r.CommonName = now, p.Addr, p.CommonName
			if save {
				h.saveLocked()
			}
			return *r, false, nil
		}
	}
	h.newRequests = slices.DeleteFunc(h.newRequests, func(t time.Time) bool { return now.Sub(t) >= RequestWindow })
	if len(h.newRequests) >= MaxNewRequests || len(h.st.Requests) >= MaxRequests {
		return ipc.RemoteRequest{}, false, errTooManyRequests
	}
	h.newRequests = append(h.newRequests, now)
	r := &ipc.RemoteRequest{ID: h.newRequestIDLocked(), Fingerprint: p.Fingerprint, CommonName: p.CommonName, Addr: p.Addr,
		FirstSeen: now, LastSeen: now, Count: 1}
	h.st.Requests = append(h.st.Requests, r)
	h.saveLocked()
	return *r, true, nil
}

// newRequestIDLocked is a short id for a request, unlike the others.
func (h *Host) newRequestIDLocked() string {
	for {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		if h.requestLocked(id) == nil {
			return id
		}
	}
}

func (h *Host) requestLocked(id string) *ipc.RemoteRequest {
	for _, r := range h.st.Requests {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// pruneLocked drops requests whose client hasn't tried for RequestTTL (h.mu
// is held).
func (h *Host) pruneLocked(now time.Time) {
	n := len(h.st.Requests)
	h.st.Requests = slices.DeleteFunc(h.st.Requests, func(r *ipc.RemoteRequest) bool { return now.Sub(r.LastSeen) >= RequestTTL })
	if len(h.st.Requests) != n {
		h.saveLocked()
	}
}

func (h *Host) dropRequestLocked(fp string) {
	n := len(h.st.Requests)
	h.st.Requests = slices.DeleteFunc(h.st.Requests, func(r *ipc.RemoteRequest) bool { return identity.Equal(r.Fingerprint, fp) })
	if len(h.st.Requests) != n {
		h.saveLocked()
	}
}

// Requests are the pending requests, oldest first.
func (h *Host) Requests() []ipc.RemoteRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked(h.o.Now())
	out := make([]ipc.RemoteRequest, 0, len(h.st.Requests))
	for _, r := range h.st.Requests {
		out = append(out, *r)
	}
	slices.SortStableFunc(out, func(a, b ipc.RemoteRequest) int { return a.FirstSeen.Compare(b.FirstSeen) })
	return out
}

// Clients are the authorized clients, as config.toml lists them, each with
// its connections.
func (h *Host) Clients() []ipc.RemoteClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := h.connectionsLocked()
	out := make([]ipc.RemoteClient, 0, len(h.remote.AuthorizedClients))
	for _, ac := range h.remote.AuthorizedClients {
		c := ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint}
		for _, rc := range conns {
			if identity.Equal(rc.Fingerprint, ac.Fingerprint) {
				c.Connections = append(c.Connections, rc)
			}
		}
		out = append(out, c)
	}
	return out
}

// Approve authorizes a request's client, under name (its cert's name when
// ""), adding it to config.toml. It gets in the next time it connects.
func (h *Host) Approve(id, name string) (ipc.RemoteClient, error) {
	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()
	h.mu.Lock()
	h.pruneLocked(h.o.Now())
	r := h.requestLocked(strings.TrimSpace(id))
	var req ipc.RemoteRequest
	if r != nil {
		req = *r
	}
	h.mu.Unlock()
	if r == nil {
		return ipc.RemoteClient{}, fmt.Errorf("no pending request %q", id)
	}
	if name == "" {
		name = req.CommonName
	}
	c, err := h.addClientLocked(req.Fingerprint, name)
	if errors.Is(err, ErrAlreadyAuthorized) {
		err = nil
	}
	if err == nil {
		h.o.Log.Printf("remote: approved request %s: %q (%s) is authorized", req.ID, c.Name, c.Fingerprint)
	}
	return c, err
}

// Deny drops a request. The client stays refused, and makes a new request
// if it tries again.
func (h *Host) Deny(id string) error {
	h.mu.Lock()
	r := h.requestLocked(strings.TrimSpace(id))
	if r != nil {
		h.dropRequestLocked(r.Fingerprint)
		h.o.Log.Printf("remote: denied request %s from %s (%q)", r.ID, r.Fingerprint, r.CommonName)
	}
	h.mu.Unlock()
	if r == nil {
		return fmt.Errorf("no pending request %q", id)
	}
	h.changed()
	return nil
}

// Authorize adds a client by its fingerprint, under name ("" names it after
// the fingerprint), to config.toml.
func (h *Host) Authorize(fingerprint, name string) (ipc.RemoteClient, error) {
	fp, err := identity.ParseFingerprint(fingerprint)
	if err != nil {
		return ipc.RemoteClient{}, err
	}
	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()
	c, err := h.addClientLocked(fp, name)
	if err == nil {
		h.o.Log.Printf("remote: %q (%s) is authorized", c.Name, c.Fingerprint)
	}
	return c, err
}

// addClientLocked puts a client on the list in config.toml, and drops its
// request (h.cfgMu is held).
func (h *Host) addClientLocked(fp, name string) (ipc.RemoteClient, error) {
	if h.o.ConfigFile == "" {
		return ipc.RemoteClient{}, ErrNoConfig
	}
	h.mu.Lock()
	if ac, ok := h.authorizedLocked(fp); ok {
		h.dropRequestLocked(fp)
		h.mu.Unlock()
		h.changed()
		return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint}, fmt.Errorf("%s is %w, as %q", fp, ErrAlreadyAuthorized, ac.Name)
	}
	ac := config.AuthorizedClient{Name: h.uniqueNameLocked(name, fp), Fingerprint: fp}
	h.mu.Unlock()
	if err := setup.AddAuthorizedClient(h.o.ConfigFile, ac); err != nil {
		return ipc.RemoteClient{}, err
	}
	h.mu.Lock()
	h.remote.AuthorizedClients = append(h.remote.AuthorizedClients, ac)
	h.dropRequestLocked(fp)
	h.mu.Unlock()
	h.changed()
	return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint}, nil
}

// uniqueNameLocked is name, or one made from the fingerprint when it's "",
// with a number added when another client has it (h.mu is held).
func (h *Host) uniqueNameLocked(name, fp string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "client-" + strings.TrimPrefix(fp, identity.Prefix)[:8]
	}
	taken := func(n string) bool {
		return slices.ContainsFunc(h.remote.AuthorizedClients, func(ac config.AuthorizedClient) bool { return ac.Name == n })
	}
	unique := name
	for i := 2; taken(unique); i++ {
		unique = name + "-" + strconv.Itoa(i)
	}
	return unique
}

// Revoke takes a client off the list in config.toml, by its name or
// fingerprint, and drops its connections at once.
func (h *Host) Revoke(client string) (ipc.RemoteClient, error) {
	if h.o.ConfigFile == "" {
		return ipc.RemoteClient{}, ErrNoConfig
	}
	h.cfgMu.Lock()
	defer h.cfgMu.Unlock()
	h.mu.Lock()
	ac, err := h.findClientLocked(client)
	h.mu.Unlock()
	if err != nil {
		return ipc.RemoteClient{}, err
	}
	if _, err := setup.RemoveAuthorizedClients(h.o.ConfigFile, func(c config.AuthorizedClient) bool {
		return identity.Equal(c.Fingerprint, ac.Fingerprint)
	}); err != nil {
		return ipc.RemoteClient{}, err
	}
	h.mu.Lock()
	h.remote.AuthorizedClients = slices.DeleteFunc(slices.Clone(h.remote.AuthorizedClients), func(c config.AuthorizedClient) bool {
		return identity.Equal(c.Fingerprint, ac.Fingerprint)
	})
	n := h.closeUnauthorizedLocked()
	h.mu.Unlock()
	h.o.Log.Printf("remote: revoked %q (%s): closed %d connections", ac.Name, ac.Fingerprint, n)
	h.changed()
	return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint}, nil
}

// findClientLocked finds an authorized client by its name or fingerprint (h.mu
// is held).
func (h *Host) findClientLocked(sel string) (config.AuthorizedClient, error) {
	sel = strings.TrimSpace(sel)
	if ac, ok := h.authorizedLocked(sel); ok {
		return ac, nil
	}
	var found []config.AuthorizedClient
	for _, ac := range h.remote.AuthorizedClients {
		if ac.Name == sel {
			found = append(found, ac)
		}
	}
	switch len(found) {
	case 0:
		return config.AuthorizedClient{}, fmt.Errorf("no authorized client %q", sel)
	case 1:
		return found[0], nil
	default:
		return config.AuthorizedClient{}, fmt.Errorf("%d clients are named %q: use the fingerprint", len(found), sel)
	}
}

// Disconnect drops connections now, by their id or their client's name or
// fingerprint, and says how many. The client stays authorized, so it can
// connect again.
func (h *Host) Disconnect(client string) (int, error) {
	sel := strings.TrimSpace(client)
	h.mu.Lock()
	n := 0
	for c, cn := range h.conns {
		if cn.id == sel || cn.name == sel || identity.Equal(c.Peer.Fingerprint, sel) {
			_ = c.Close()
			n++
		}
	}
	h.mu.Unlock()
	if n == 0 {
		return 0, fmt.Errorf("no client %q is connected", client)
	}
	h.o.Log.Printf("remote: disconnected %q: closed %d connections", sel, n)
	return n, nil
}

// SetHosting turns Hosting on or off in config.toml, and applies it.
func (h *Host) SetHosting(on bool) (ipc.RemoteStatus, error) {
	if h.o.ConfigFile == "" {
		return ipc.RemoteStatus{}, ErrNoConfig
	}
	h.cfgMu.Lock()
	err := setup.SaveSetting(h.o.ConfigFile, "remote.host", "enabled", on)
	if err == nil {
		err = h.reloadLocked(true)
	}
	h.cfgMu.Unlock()
	return h.Status(), err
}
