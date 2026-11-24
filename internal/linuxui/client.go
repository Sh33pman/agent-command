package linuxui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

const (
	// CallTimeout is above the daemon's 15s Remote host wait (design 8.2).
	CallTimeout = 30 * time.Second
	maxPoolSize = 4
)

// Options configures a Linux UI Client.
type Options struct {
	Socket   string
	ClientID string
	Device   string
	Version  string
}

// Client talks to the acmd daemon over Protocol v2 with a pooled request
// connection and a dedicated untimed event stream.
type Client struct {
	opts Options

	mu   sync.Mutex
	pool []*ipc.Conn
}

// NewClient creates a Client, resolving defaults for Socket, ClientID, Device, and Version.
func NewClient(opts Options) (*Client, error) {
	if opts.Socket == "" {
		p, err := paths.Default()
		if err != nil {
			return nil, err
		}
		opts.Socket = p.Socket()
	}
	if opts.Device == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			opts.Device = h
		} else {
			opts.Device = "linux"
		}
	}
	if opts.ClientID == "" {
		opts.ClientID = "linux-" + opts.Device
	}
	if opts.Version == "" {
		opts.Version = version.Version
	}
	return &Client{opts: opts}, nil
}

// ID returns this client's registered ID.
func (c *Client) ID() string { return c.opts.ClientID }

func (c *Client) hello() ipc.Hello {
	return ipc.Hello{
		Proto:    ipc.Proto,
		ProtoMin: ipc.ProtoMin,
		ProtoMax: ipc.Proto,
		Version:  c.opts.Version,
		Client: &ipc.ClientHello{
			ID:     c.opts.ClientID,
			Kind:   ipc.KindLinux,
			Device: c.opts.Device,
			Caps:   []string{ipc.CapNotify, ipc.CapAttach},
			OnHost: true,
		},
	}
}

func (c *Client) dial(ctx context.Context) (*ipc.Conn, error) {
	conn, err := ipc.Dial(ctx, c.opts.Socket, c.hello())
	var mismatch *ipc.ErrVersionMismatch
	if errors.As(err, &mismatch) && conn != nil {
		// Protocol v2 was already agreed in Hello; allow differing build versions
		// (e.g. a dev UI binary connecting to an installed acmd daemon).
		return conn, nil
	}
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, err
	}
	return conn, nil
}

func (c *Client) acquire(ctx context.Context) (*ipc.Conn, error) {
	c.mu.Lock()
	if n := len(c.pool); n > 0 {
		conn := c.pool[n-1]
		c.pool = c.pool[:n-1]
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()
	return c.dial(ctx)
}

func (c *Client) release(conn *ipc.Conn) {
	if conn == nil {
		return
	}
	c.mu.Lock()
	if len(c.pool) < maxPoolSize {
		c.pool = append(c.pool, conn)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	_ = conn.Close()
}

// Close closes all pooled connections.
func (c *Client) Close() {
	c.mu.Lock()
	pool := c.pool
	c.pool = nil
	c.mu.Unlock()
	for _, conn := range pool {
		_ = conn.Close()
	}
}

// Call sends an op with a 30s timeout using the connection pool.
func (c *Client) Call(ctx context.Context, op string, in, out any) error {
	return c.CallOnce(ctx, op, "", in, out)
}

// CallOnce sends an op with an optional request_id for idempotent retries.
func (c *Client) CallOnce(ctx context.Context, op, requestID string, in, out any) error {
	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	conn, err := c.acquire(callCtx)
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(CallTimeout))
	err = conn.CallOnce(op, requestID, in, out)
	if err != nil {
		_ = conn.Close()
		// Retry once on a fresh connection when the pooled one went stale
		// and either it's a read or we have a requestID.
		if requestID != "" || op == ipc.OpSnapshot || op == ipc.OpHarnesses {
			conn2, err2 := c.dial(callCtx)
			if err2 != nil {
				return err
			}
			_ = conn2.SetDeadline(time.Now().Add(CallTimeout))
			err2 = conn2.CallOnce(op, requestID, in, out)
			if err2 != nil {
				_ = conn2.Close()
				return err2
			}
			_ = conn2.SetDeadline(time.Time{})
			c.release(conn2)
			return nil
		}
		return err
	}
	_ = conn.SetDeadline(time.Time{})
	c.release(conn)
	return nil
}

// SetPresence reports this client's presence and focused session ID.
func (c *Client) SetPresence(ctx context.Context, presence, focus string) error {
	return c.Call(ctx, ipc.OpClientPresence, ipc.ClientPresence{
		Presence: presence,
		Focus:    focus,
	}, nil)
}

// Subscribe opens a dedicated untimed event stream starting from since.
func (c *Client) Subscribe(ctx context.Context, since uint64) (*ipc.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	conn, err := c.dial(dialCtx)
	if err != nil {
		return nil, err
	}
	if err := conn.Subscribe(ipc.SubscribeRequest{Since: since}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Clear the dial deadline so the long-lived stream stays open.
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// Store holds the current daemon Snapshot and revision, updated from Event lines.
type Store struct {
	mu   sync.RWMutex
	snap ipc.Snapshot
	rev  uint64
	has  bool
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{}
}

// Snapshot returns a shallow copy of the current Snapshot, its revision, and whether one was loaded.
func (s *Store) Snapshot() (ipc.Snapshot, uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap, s.rev, s.has
}

// Apply updates the Store with one event from the daemon stream.
// Returns true if the snapshot changed in a way callers should re-render.
func (s *Store) Apply(ev ipc.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ev.Rev > 0 {
		s.rev = ev.Rev
	}
	switch ev.Type {
	case ipc.EventSnapshot, ipc.EventResync:
		if ev.Snapshot == nil {
			return false, nil
		}
		s.snap = *ev.Snapshot
		s.has = true
		return true, nil
	case ipc.EventAdded, ipc.EventUpdated:
		return s.applyUpsert(ev)
	case ipc.EventRemoved:
		return s.applyRemove(ev), nil
	default:
		return false, nil
	}
}

func (s *Store) applyUpsert(ev ipc.Event) (bool, error) {
	if len(ev.Data) == 0 {
		return false, nil
	}
	switch ev.Entity {
	case ipc.EntitySession:
		var item ipc.Session
		if err := json.Unmarshal(ev.Data, &item); err != nil {
			return false, fmt.Errorf("decode session: %w", err)
		}
		s.snap.Sessions = upsertByID(s.snap.Sessions, &item, func(v *ipc.Session) string { return v.ID + "\x00" + v.Host })
		return true, nil
	case ipc.EntityGroup:
		var item ipc.Group
		if err := json.Unmarshal(ev.Data, &item); err != nil {
			return false, fmt.Errorf("decode group: %w", err)
		}
		s.snap.Groups = upsertByID(s.snap.Groups, &item, func(v *ipc.Group) string { return v.ID + "\x00" + v.Host })
		return true, nil
	case ipc.EntitySet:
		var item ipc.Set
		if err := json.Unmarshal(ev.Data, &item); err != nil {
			return false, fmt.Errorf("decode set: %w", err)
		}
		s.snap.Sets = upsertByID(s.snap.Sets, item, func(v ipc.Set) string { return v.ID + "\x00" + v.Host })
		return true, nil
	case ipc.EntityHarness:
		var item ipc.Harness
		if err := json.Unmarshal(ev.Data, &item); err != nil {
			return false, fmt.Errorf("decode harness: %w", err)
		}
		s.snap.Harnesses = upsertByID(s.snap.Harnesses, item, func(v ipc.Harness) string { return v.ID + "\x00" + v.Host })
		return true, nil
	case ipc.EntityInbox:
		var item core.InboxItem
		if err := json.Unmarshal(ev.Data, &item); err != nil {
			return false, fmt.Errorf("decode inbox: %w", err)
		}
		s.snap.Inbox = upsertByID(s.snap.Inbox, item, func(v core.InboxItem) string { return strconv.FormatInt(v.ID, 10) })
		return true, nil
	case ipc.EntityNotice:
		var item ipc.Notice
		if err := json.Unmarshal(ev.Data, &item); err != nil {
			return false, fmt.Errorf("decode notice: %w", err)
		}
		s.snap.Notices = append(s.snap.Notices, item)
		if len(s.snap.Notices) > 100 {
			s.snap.Notices = s.snap.Notices[len(s.snap.Notices)-100:]
		}
		return true, nil
	case ipc.EntityView:
		var vs ipc.ViewState
		if err := json.Unmarshal(ev.Data, &vs); err != nil {
			return false, fmt.Errorf("decode view: %w", err)
		}
		s.snap.ViewState = vs
		return true, nil
	case ipc.EntitySettings:
		var m map[string]any
		if err := json.Unmarshal(ev.Data, &m); err != nil {
			return false, fmt.Errorf("decode settings: %w", err)
		}
		s.snap.Settings = m
		return true, nil
	case ipc.EntityHealth:
		var list []ipc.Health
		if err := json.Unmarshal(ev.Data, &list); err != nil {
			return false, fmt.Errorf("decode health: %w", err)
		}
		s.snap.Health = list
		return true, nil
	}
	return false, nil
}

func (s *Store) applyRemove(ev ipc.Event) bool {
	key := ev.ID + "\x00" + ev.Host
	switch ev.Entity {
	case ipc.EntitySession:
		s.snap.Sessions = removeByID(s.snap.Sessions, key, func(v *ipc.Session) string { return v.ID + "\x00" + v.Host })
		return true
	case ipc.EntityGroup:
		s.snap.Groups = removeByID(s.snap.Groups, key, func(v *ipc.Group) string { return v.ID + "\x00" + v.Host })
		return true
	case ipc.EntitySet:
		s.snap.Sets = removeByID(s.snap.Sets, key, func(v ipc.Set) string { return v.ID + "\x00" + v.Host })
		return true
	case ipc.EntityHarness:
		s.snap.Harnesses = removeByID(s.snap.Harnesses, key, func(v ipc.Harness) string { return v.ID + "\x00" + v.Host })
		return true
	case ipc.EntityInbox:
		s.snap.Inbox = removeByID(s.snap.Inbox, ev.ID, func(v core.InboxItem) string { return strconv.FormatInt(v.ID, 10) })
		return true
	}
	return false
}

func upsertByID[T any](items []T, item T, keyFn func(T) string) []T {
	k := keyFn(item)
	for i := range items {
		if keyFn(items[i]) == k {
			items[i] = item
			return items
		}
	}
	return append(items, item)
}

func removeByID[T any](items []T, key string, keyFn func(T) string) []T {
	out := items[:0]
	for _, it := range items {
		if keyFn(it) != key {
			out = append(out, it)
		}
	}
	return out
}

// RunStream subscribes to the daemon and delivers snapshot/event notifications
// to onEvent until ctx is cancelled. Reconnects with backoff on drop.
func (c *Client) RunStream(ctx context.Context, store *Store, onEvent func(ipc.Event), onStatus func(string, error)) {
	backoff := 250 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		_, since, has := store.Snapshot()
		if !has {
			since = 0
		}
		conn, err := c.Subscribe(ctx, since)
		if err != nil {
			if onStatus != nil {
				onStatus("disconnected", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 5*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 250 * time.Millisecond
		if onStatus != nil {
			onStatus("connected", nil)
		}
		if err := readStream(ctx, conn, store, onEvent); err != nil && !errors.Is(err, context.Canceled) {
			if onStatus != nil {
				onStatus("reconnecting", err)
			}
		}
		_ = conn.Close()
	}
}

func readStream(ctx context.Context, conn *ipc.Conn, store *Store, onEvent func(ipc.Event)) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	for {
		ev, err := conn.NextEvent()
		if err != nil {
			return err
		}
		changed, applyErr := store.Apply(ev)
		if applyErr != nil {
			continue
		}
		if changed && onEvent != nil {
			onEvent(ev)
		}
	}
}
