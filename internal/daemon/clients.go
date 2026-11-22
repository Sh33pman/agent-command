package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// The client registry: who uses this daemon, how, and who shows each notice
// (notify.route). Each client says who it is in its hello, on every
// connection; one without an id (the CLI, a hook) isn't registered.

// clientGap is how long after its last connection closes a client still
// counts as connected: it opens the next one straight after.
const clientGap = 5 * time.Second

// forgetEvery is how often clients not seen for clients.forget_after are
// forgotten.
const forgetEvery = 6 * time.Hour

// desktopKinds are clients on a computer, which show a notice when the one
// you're using can't.
// A paired machine is one too: its daemon shows it on its UI you're using.
var desktopKinds = []string{ipc.KindMac, ipc.KindLinux, ipc.KindWindows, ipc.KindTUI, ipc.KindMachine}

type registered struct {
	store.Client
	conns   int       // open connections
	gone    time.Time // when the last one closed
	profile ipc.ClientProfile
}

type registry struct {
	mu      sync.Mutex
	clients map[string]*registered
}

// caller is who sent a request: the client named in its connection's hello,
// the process on the other end of the socket, or a paired device (caller.go).
type caller struct {
	clientID string
	pid      int
	remote   string // the paired device, for a call from another machine
	// machine is the paired machine a call came from, by its fingerprint:
	// its registered client here (machineConnected). clientID is then the
	// UI on it that asked, when it said.
	machine string
	device  string // that UI's device and kind, for the record
	once    sync.Once
	line    lineage
}

type callerKey struct{}

func callerOf(ctx context.Context) *caller {
	if c, ok := ctx.Value(callerKey{}).(*caller); ok {
		return c
	}
	return &caller{}
}

// loadClients reads the registry.
func (d *Daemon) loadClients(ctx context.Context) error {
	cs, err := d.store.Clients(ctx)
	if err != nil {
		return err
	}
	d.reg = &registry{clients: map[string]*registered{}}
	for _, c := range cs {
		r := &registered{Client: c}
		d.clientState(ctx, r)
		d.reg.clients[c.ID] = r
	}
	return nil
}

// hello registers the client a connection's hello names, and returns a
// function that marks the connection closed.
func (d *Daemon) hello(ctx context.Context, h *ipc.ClientHello, who *caller) func() {
	if h == nil || h.ID == "" || d.reg == nil {
		return func() {}
	}
	c := store.Client{ID: h.ID, Kind: h.Kind, Device: h.Device, Caps: h.Caps, OnHost: h.OnHost}
	if err := d.store.SaveClient(ctx, c); err != nil {
		d.cfg.Log.Printf("clients: %v", err)
	}
	now := d.cfg.Now()
	d.reg.mu.Lock()
	r := d.reg.clients[h.ID]
	if r == nil {
		r = &registered{Client: c}
		r.FirstSeen = now
		d.reg.clients[h.ID] = r
	}
	r.Kind, r.Device, r.Caps, r.OnHost, r.LastSeen = c.Kind, c.Device, c.Caps, c.OnHost, now
	r.conns++
	d.reg.mu.Unlock()
	who.clientID = h.ID
	d.tellHosts()
	return func() {
		d.reg.mu.Lock()
		r.conns--
		r.gone, r.LastSeen = d.cfg.Now(), d.cfg.Now()
		d.reg.mu.Unlock()
		_ = d.store.TouchClient(context.WithoutCancel(ctx), h.ID)
		d.tellHosts()
	}
}

func (d *Daemon) connectedLocked(r *registered) bool {
	return r.conns > 0 || (!r.gone.IsZero() && d.cfg.Now().Sub(r.gone) < clientGap)
}

// presence records how a client is used now: a UI on this machine, or a
// paired machine as a whole.
func (d *Daemon) presence(ctx context.Context, p ipc.ClientPresence) error {
	who := callerOf(ctx)
	id := cmp.Or(who.machine, who.clientID)
	if id == "" || d.reg == nil {
		return invalid("say who you are in the hello first")
	}
	switch p.Presence {
	case ipc.PresenceActive, ipc.PresenceIdle, ipc.PresenceBackground:
	default:
		return invalid("presence is active, idle or background, not %q", p.Presence)
	}
	active := p.Presence == ipc.PresenceActive
	if err := d.store.SetPresence(ctx, id, p.Presence, p.Focus, active); err != nil {
		return err
	}
	d.reg.mu.Lock()
	if r := d.reg.clients[id]; r != nil {
		r.Presence, r.Focus, r.LastSeen = p.Presence, p.Focus, d.cfg.Now()
		if active {
			r.LastActive = d.cfg.Now()
		}
	}
	d.reg.mu.Unlock()
	if who.machine == "" {
		d.tellHosts() // a UI here: this machine's hosts hear of it
	}
	return nil
}

// machineConnected registers a paired machine while its connection lasts
// (design 6.9, 6.12): its daemon stands for its UIs, so the host routes a
// notice to the machine you're using, and that machine routes it on to its
// UI. It's known by its fingerprint, named as its pairing is.
func (d *Daemon) machineConnected(fingerprint, name string) func() {
	if d.reg == nil || fingerprint == "" {
		return func() {}
	}
	ctx := context.Background()
	c := store.Client{ID: fingerprint, Kind: ipc.KindMachine, Device: name, Caps: []string{ipc.CapNotify}}
	if err := d.store.SaveClient(ctx, c); err != nil {
		d.cfg.Log.Printf("clients: %v", err)
	}
	now := d.cfg.Now()
	d.reg.mu.Lock()
	r := d.reg.clients[fingerprint]
	if r == nil {
		r = &registered{Client: c}
		r.FirstSeen = now
		d.reg.clients[fingerprint] = r
	}
	r.Kind, r.Device, r.Caps, r.LastSeen = c.Kind, c.Device, c.Caps, now
	r.conns++
	d.reg.mu.Unlock()
	return func() {
		d.reg.mu.Lock()
		r.conns--
		r.gone, r.LastSeen = d.cfg.Now(), d.cfg.Now()
		d.reg.mu.Unlock()
		_ = d.store.TouchClient(ctx, fingerprint)
	}
}

// machinePresence is how this machine is used now, for its hosts: active
// while any of its UIs is, else idle while any is open, else background.
func (d *Daemon) machinePresence() string {
	if d.reg == nil {
		return ipc.PresenceBackground
	}
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	out := ipc.PresenceBackground
	for _, r := range d.reg.clients {
		if r.Kind == ipc.KindMachine || !d.connectedLocked(r) || r.conns == 0 {
			continue
		}
		switch r.Presence {
		case ipc.PresenceActive:
			return ipc.PresenceActive
		case ipc.PresenceIdle:
			out = ipc.PresenceIdle
		}
	}
	return out
}

// tellHosts passes this machine's presence on to its hosts.
func (d *Daemon) tellHosts() {
	if d.client != nil {
		d.client.SetPresence(d.machinePresence())
	}
}

// uiOf is the UI a request came from, to name on a host: its id, kind and
// device. nil when the caller didn't say who it is.
func (d *Daemon) uiOf(ctx context.Context) *ipc.ClientHello {
	id := callerOf(ctx).clientID
	if id == "" || d.reg == nil {
		return nil
	}
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	if r := d.reg.clients[id]; r != nil {
		return &ipc.ClientHello{ID: r.ID, Kind: r.Kind, Device: r.Device}
	}
	return &ipc.ClientHello{ID: id}
}

// clientList is the registry, the most recently seen first.
func (d *Daemon) clientList() []ipc.Client {
	out := []ipc.Client{}
	if d.reg == nil {
		return out
	}
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	for _, r := range d.reg.clients {
		c := ipc.Client{ClientHello: ipc.ClientHello{ID: r.ID, Kind: r.Kind, Device: r.Device, Caps: r.Caps, OnHost: r.OnHost},
			Presence: r.Presence, Focus: r.Focus, Connected: d.connectedLocked(r),
			FirstSeen: r.FirstSeen, LastSeen: r.LastSeen, LastActive: r.LastActive}
		if !c.Connected {
			c.Presence = ipc.PresenceDisconnected
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b ipc.Client) int { return cmp.Or(b.LastSeen.Compare(a.LastSeen), cmp.Compare(a.ID, b.ID)) })
	return out
}

// route picks the clients that show a notice: with RouteAll every connected
// client that shows notices; with RouteOne the one you're using, else the
// desktop client used most recently. None: the host shows it itself.
func (d *Daemon) route(how, kind string) []string {
	if d.reg == nil {
		return nil
	}
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	var can []*registered
	for _, r := range d.reg.clients {
		if d.connectedLocked(r) && slices.Contains(r.Caps, ipc.CapNotify) && (kind == "withdraw" || r.wants(kind)) {
			can = append(can, r)
		}
	}
	// The most recently used first.
	slices.SortFunc(can, func(a, b *registered) int {
		return cmp.Or(b.LastActive.Compare(a.LastActive), b.LastSeen.Compare(a.LastSeen), cmp.Compare(a.ID, b.ID))
	})
	if how == config.RouteAll {
		out := make([]string, 0, len(can))
		for _, r := range can {
			out = append(out, r.ID)
		}
		slices.Sort(out)
		return out
	}
	for _, r := range can {
		if r.Presence == ipc.PresenceActive {
			return []string{r.ID}
		}
	}
	for _, r := range can {
		if slices.Contains(desktopKinds, r.Kind) {
			return []string{r.ID}
		}
	}
	return nil
}

// noticeByID is a recent notice.
func (d *Daemon) noticeByID(ref ipc.NoticeRef) (ipc.Notice, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, n := range d.notices() {
		if n.Seq == ref.ID {
			return n, nil
		}
	}
	return ipc.Notice{}, &codedError{code: ipc.CodeNotFound, err: invalid("no recent notice %d", ref.ID)}
}

// forgetClients forgets the clients not seen for clients.forget_after, unless
// that's 0, and says how many.
func (d *Daemon) forgetClients(ctx context.Context) int {
	after := d.config().Clients.ForgetAfterDuration()
	if after <= 0 || d.reg == nil {
		return 0
	}
	ids, err := d.store.ForgetClients(ctx, d.cfg.Now().Add(-after))
	if err != nil {
		d.cfg.Log.Printf("clients: %v", err)
		return 0
	}
	d.reg.mu.Lock()
	for _, id := range ids {
		if r := d.reg.clients[id]; r != nil && r.conns == 0 {
			delete(d.reg.clients, id)
		}
	}
	d.reg.mu.Unlock()
	return len(ids)
}

// keepForgetting forgets old clients now and every forgetEvery.
func (d *Daemon) keepForgetting(ctx context.Context) {
	d.forgetClients(ctx)
	t := time.NewTicker(forgetEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			return
		case <-t.C:
			d.forgetClients(ctx)
		}
	}
}

func (d *Daemon) handleClients(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpClients:
		return d.clientList(), nil
	case ipc.OpClientPresence:
		p, err := decode[ipc.ClientPresence](req.Data)
		if err != nil {
			return nil, err
		}
		return nil, d.presence(ctx, p)
	case ipc.OpNoticeGet:
		r, err := decode[ipc.NoticeRef](req.Data)
		if err != nil {
			return nil, err
		}
		return d.noticeByID(r)
	case ipc.OpClientProfile:
		return d.profile(ctx)
	case ipc.OpClientProfileSet:
		p, err := decode[ipc.ClientProfile](req.Data)
		if err != nil {
			return nil, err
		}
		return d.setProfile(ctx, p)
	case ipc.OpKeysSet:
		return nil, d.setKeymap(ctx, req.Data)
	case ipc.OpKeys:
		var r ipc.KeysRequest
		if len(req.Data) > 0 {
			var err error
			if r, err = decode[ipc.KeysRequest](req.Data); err != nil {
				return nil, err
			}
		}
		return d.keymap(ctx, r)
	}
	return nil, nil
}

// helloOf reads a hello's client, if it names one.
func helloOf(data json.RawMessage) *ipc.ClientHello {
	var h ipc.Hello
	if json.Unmarshal(data, &h) != nil {
		return nil
	}
	return h.Client
}

// wants says whether a client shows notices of kind.
func (r *registered) wants(kind string) bool {
	return len(r.profile.Notify) == 0 || slices.Contains(r.profile.Notify, kind)
}

// clientState reads what a registered client keeps into the registry.
func (d *Daemon) clientState(ctx context.Context, r *registered) {
	if v, err := d.store.ClientField(ctx, r.ID, store.ClientProfile); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &r.profile)
	}
}

func (d *Daemon) callerID(ctx context.Context) (string, error) {
	id := callerOf(ctx).clientID
	if id == "" || d.reg == nil {
		return "", invalid("say who you are in the hello first")
	}
	return id, nil
}

// setProfile changes the calling client's notification profile.
func (d *Daemon) setProfile(ctx context.Context, p ipc.ClientProfile) (ipc.ClientProfile, error) {
	id, err := d.callerID(ctx)
	if err != nil {
		return ipc.ClientProfile{}, err
	}
	for _, k := range p.Notify {
		switch k {
		case "waiting", "error", "done", "paused":
		default:
			return ipc.ClientProfile{}, invalid("a notice is waiting, error, done or paused, not %q", k)
		}
	}
	switch p.PopupOpens {
	case "", ipc.PopupNeedsYou, ipc.PopupEverything, ipc.PopupNever:
	default:
		return ipc.ClientProfile{}, invalid("the popup opens for needs_you, everything or never, not %q", p.PopupOpens)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ipc.ClientProfile{}, err
	}
	if err := d.store.SetClientField(ctx, id, store.ClientProfile, string(b)); err != nil {
		return ipc.ClientProfile{}, err
	}
	d.reg.mu.Lock()
	if r := d.reg.clients[id]; r != nil {
		r.profile = p
	}
	d.reg.mu.Unlock()
	return p, nil
}

// profile is the calling client's notification profile.
func (d *Daemon) profile(ctx context.Context) (ipc.ClientProfile, error) {
	id, err := d.callerID(ctx)
	if err != nil {
		return ipc.ClientProfile{}, err
	}
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	if r := d.reg.clients[id]; r != nil {
		return r.profile, nil
	}
	return ipc.ClientProfile{}, nil
}

// setKeymap registers the calling client's keymap.
func (d *Daemon) setKeymap(ctx context.Context, m json.RawMessage) error {
	id, err := d.callerID(ctx)
	if err != nil {
		return err
	}
	var obj map[string]any
	if json.Unmarshal(m, &obj) != nil {
		return invalid("a keymap is a JSON object")
	}
	return d.store.SetClientField(ctx, id, store.ClientKeymap, string(m))
}

// keymap is a client's keymap: the one named, or the desktop client's seen
// most recently, else any client's.
func (d *Daemon) keymap(ctx context.Context, r ipc.KeysRequest) (ipc.Keymap, error) {
	cs, vs, err := d.store.ClientsWith(ctx, store.ClientKeymap)
	if err != nil {
		return ipc.Keymap{}, err
	}
	pick := -1
	for i, c := range cs {
		switch {
		case r.ClientID != "":
			if c.ID == r.ClientID {
				pick = i
			}
		case pick < 0 && slices.Contains(desktopKinds, c.Kind):
			pick = i
		}
	}
	if pick < 0 && r.ClientID == "" && len(cs) > 0 {
		pick = 0
	}
	if pick < 0 {
		return ipc.Keymap{}, &codedError{code: ipc.CodeNotFound,
			err: invalid("no app has listed its shortcuts yet: open Agent Command once, then try again")}
	}
	c := cs[pick]
	return ipc.Keymap{ClientID: c.ID, Kind: c.Kind, Device: c.Device, Map: json.RawMessage(vs[pick])}, nil
}
