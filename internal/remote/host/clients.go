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

// ErrNoConfig is a change to settings with nowhere to make it.
var ErrNoConfig = errors.New("hosting has no settings to change")

// ErrInConfig is a change to a client that config.toml lists: it's changed
// by editing the file.
var ErrInConfig = errors.New("set in config.toml: edit the file to change it")

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
func (h *Host) authorizedLocked(fp string) (authorized, bool) {
	for _, ac := range h.clientsLocked() {
		if identity.Equal(ac.Fingerprint, fp) {
			return ac, true
		}
	}
	return authorized{}, false
}

// authorized is a client on the list, and whether config.toml lists it.
type authorized struct {
	config.AuthorizedClient
	inConfig bool
}

// clientsLocked is the list: config.toml's clients, then the ones authorized
// through ops (h.mu is held).
func (h *Host) clientsLocked() []authorized {
	out := make([]authorized, 0, len(h.remote.AuthorizedClients)+len(h.added))
	for _, ac := range h.remote.AuthorizedClients {
		if ac.Scope == "" { // listed before scopes: everything it could do
			ac.Scope = config.ScopeAdmin
		}
		out = append(out, authorized{ac, true})
	}
	for _, ac := range h.added {
		if !slices.ContainsFunc(out, func(a authorized) bool { return identity.Equal(a.Fingerprint, ac.Fingerprint) }) {
			out = append(out, authorized{ac, false})
		}
	}
	return out
}

// closeUnauthorizedLocked drops the connections of clients no longer on the
// list (h.mu is held).
func (h *Host) closeUnauthorizedLocked() int {
	n := 0
	for c, cn := range h.conns {
		ac, ok := h.authorizedLocked(c.Peer.Fingerprint)
		if !ok {
			h.dropLocked(c)
			n++
			continue
		}
		cn.name = ac.Name // a rename
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

// Clients are the authorized clients, config.toml's first, each with its
// connections.
func (h *Host) Clients() []ipc.RemoteClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := h.connectionsLocked()
	list := h.clientsLocked()
	out := make([]ipc.RemoteClient, 0, len(list))
	for _, ac := range list {
		c := ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint, InConfig: ac.inConfig, Scope: ac.ScopeOf()}
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
// ""). It gets in the next time it connects.
func (h *Host) Approve(id, name string) (ipc.RemoteClient, error) {
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
// the fingerprint).
func (h *Host) Authorize(fingerprint, name string) (ipc.RemoteClient, error) {
	fp, err := identity.ParseFingerprint(fingerprint)
	if err != nil {
		return ipc.RemoteClient{}, err
	}
	c, err := h.addClientLocked(fp, name)
	if err == nil {
		h.o.Log.Printf("remote: %q (%s) is authorized", c.Name, c.Fingerprint)
	}
	return c, err
}

// addClientLocked puts a client on the list, and drops its request.
func (h *Host) addClientLocked(fp, name string) (ipc.RemoteClient, error) {
	h.mu.Lock()
	if ac, ok := h.authorizedLocked(fp); ok {
		h.dropRequestLocked(fp)
		h.mu.Unlock()
		h.changed()
		return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint, InConfig: ac.inConfig},
			fmt.Errorf("%s is %w, as %q", fp, ErrAlreadyAuthorized, ac.Name)
	}
	ac := config.AuthorizedClient{Name: h.uniqueNameLocked(name, fp, ""), Fingerprint: fp}
	if err := h.o.Data.AddAuthorizedClient(ac); err != nil {
		h.mu.Unlock()
		return ipc.RemoteClient{}, err
	}
	h.added = append(h.added, ac)
	h.dropRequestLocked(fp)
	h.mu.Unlock()
	h.changed()
	return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint}, nil
}

// uniqueNameLocked is name, or one made from the fingerprint when it's "",
// with a number added when another client has it (h.mu is held). The
// client with fingerprint self doesn't count.
func (h *Host) uniqueNameLocked(name, fp, self string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "client-" + strings.TrimPrefix(fp, identity.Prefix)[:8]
	}
	list := h.clientsLocked()
	taken := func(n string) bool {
		return slices.ContainsFunc(list, func(ac authorized) bool { return ac.Name == n && !identity.Equal(ac.Fingerprint, self) })
	}
	unique := name
	for i := 2; taken(unique); i++ {
		unique = name + "-" + strconv.Itoa(i)
	}
	return unique
}

// Revoke takes a client off the list, by its name or fingerprint, and drops
// its connections at once. One config.toml lists is taken off there.
func (h *Host) Revoke(client string) (ipc.RemoteClient, error) {
	h.mu.Lock()
	ac, err := h.findClientLocked(client)
	if err == nil && ac.inConfig {
		err = fmt.Errorf("%q is %w", ac.Name, ErrInConfig)
	}
	if err == nil {
		_, err = h.o.Data.RemoveAuthorizedClient(ac.Fingerprint)
	}
	if err != nil {
		h.mu.Unlock()
		return ipc.RemoteClient{}, err
	}
	h.added = slices.DeleteFunc(h.added, func(c config.AuthorizedClient) bool { return identity.Equal(c.Fingerprint, ac.Fingerprint) })
	n := h.closeUnauthorizedLocked()
	h.mu.Unlock()
	h.o.Log.Printf("remote: revoked %q (%s): closed %d connections", ac.Name, ac.Fingerprint, n)
	h.changed()
	return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint}, nil
}

// Rename gives an authorized client, by its name or fingerprint, a new name.
// It stays authorized and connected.
func (h *Host) Rename(client, name string) (ipc.RemoteClient, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ipc.RemoteClient{}, errors.New("the new name can't be empty")
	}
	h.mu.Lock()
	ac, err := h.findClientLocked(client)
	switch {
	case err != nil:
	case ac.inConfig:
		err = fmt.Errorf("%q is %w", ac.Name, ErrInConfig)
	case h.uniqueNameLocked(name, ac.Fingerprint, ac.Fingerprint) != name:
		err = fmt.Errorf("another client is already called %q", name)
	}
	if err == nil {
		err = h.o.Data.AddAuthorizedClient(config.AuthorizedClient{Name: name, Fingerprint: ac.Fingerprint})
	}
	if err != nil {
		h.mu.Unlock()
		return ipc.RemoteClient{}, err
	}
	for i := range h.added {
		if identity.Equal(h.added[i].Fingerprint, ac.Fingerprint) {
			h.added[i].Name = name
		}
	}
	h.closeUnauthorizedLocked() // names the connections again
	h.mu.Unlock()
	h.o.Log.Printf("remote: renamed %q to %q (%s)", ac.Name, name, ac.Fingerprint)
	h.changed()
	return ipc.RemoteClient{Name: name, Fingerprint: ac.Fingerprint}, nil
}

// findClientLocked finds an authorized client by its name or fingerprint (h.mu
// is held).
func (h *Host) findClientLocked(sel string) (authorized, error) {
	sel = strings.TrimSpace(sel)
	if ac, ok := h.authorizedLocked(sel); ok {
		return ac, nil
	}
	var found []authorized
	for _, ac := range h.clientsLocked() {
		if ac.Name == sel {
			found = append(found, ac)
		}
	}
	switch len(found) {
	case 0:
		return authorized{}, fmt.Errorf("no authorized client %q", sel)
	case 1:
		return found[0], nil
	default:
		return authorized{}, fmt.Errorf("%d clients are named %q: use the fingerprint", len(found), sel)
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
			h.dropLocked(c)
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

// SetHosting turns Hosting on or off in the settings, and applies it.
func (h *Host) SetHosting(on bool) (ipc.RemoteStatus, error) {
	if h.o.SetSettings == nil {
		return ipc.RemoteStatus{}, ErrNoConfig
	}
	err := h.o.SetSettings(map[string]any{"remote.host.enabled": on})
	if err == nil {
		h.Reload()
	}
	return h.Status(), err
}

// Scope is what the client with this fingerprint may do here: "" when it
// isn't authorized.
func (h *Host) Scope(fingerprint string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ac, ok := h.authorizedLocked(fingerprint)
	if !ok {
		return ""
	}
	return ac.ScopeOf()
}

// SetScope changes what an authorized client may do here: read, act or
// admin.
func (h *Host) SetScope(client, scope string) (ipc.RemoteClient, error) {
	switch scope {
	case config.ScopeRead, config.ScopeAct, config.ScopeAdmin:
	default:
		return ipc.RemoteClient{}, fmt.Errorf("a client may read, act or admin, not %q", scope)
	}
	h.mu.Lock()
	ac, err := h.findClientLocked(client)
	if err == nil && ac.inConfig {
		err = fmt.Errorf("%q is %w", ac.Name, ErrInConfig)
	}
	if err == nil {
		err = h.o.Data.AddAuthorizedClient(config.AuthorizedClient{Name: ac.Name, Fingerprint: ac.Fingerprint, Scope: scope})
	}
	if err != nil {
		h.mu.Unlock()
		return ipc.RemoteClient{}, err
	}
	for i := range h.added {
		if identity.Equal(h.added[i].Fingerprint, ac.Fingerprint) {
			h.added[i].Scope = scope
		}
	}
	h.mu.Unlock()
	h.o.Log.Printf("remote: %q (%s) may now %s", ac.Name, ac.Fingerprint, scope)
	h.changed()
	return ipc.RemoteClient{Name: ac.Name, Fingerprint: ac.Fingerprint, Scope: scope}, nil
}
