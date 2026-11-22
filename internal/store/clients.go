package store

import (
	"context"
	"strings"
	"time"
)

// Client is one client in the registry.
type Client struct {
	ID       string
	Kind     string
	Device   string
	Caps     []string
	OnHost   bool
	Presence string
	Focus    string // the session it shows, when it says
	// FirstSeen, LastSeen and LastActive: when it first and last connected,
	// and when the person last used it.
	FirstSeen, LastSeen, LastActive time.Time
}

// Clients lists the registry, the most recently seen first.
func (s *Store) Clients(ctx context.Context) ([]Client, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, kind, device, caps, on_host, presence, focus, first_seen, last_seen, last_active
		FROM clients ORDER BY last_seen DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var c Client
		var caps string
		var onHost int
		var first, last, active int64
		if err := rows.Scan(&c.ID, &c.Kind, &c.Device, &caps, &onHost, &c.Presence, &c.Focus, &first, &last, &active); err != nil {
			return nil, err
		}
		if caps != "" {
			c.Caps = strings.Split(caps, ",")
		}
		c.OnHost = onHost == 1
		c.FirstSeen, c.LastSeen = fromMS(first), fromMS(last)
		if active > 0 {
			c.LastActive = fromMS(active)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveClient adds a client, or updates what it says about itself, as seen
// now. Its presence and when it was last active are kept.
func (s *Store) SaveClient(ctx context.Context, c Client) error {
	now := ms(s.now().UTC())
	onHost := 0
	if c.OnHost {
		onHost = 1
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO clients (id, kind, device, caps, on_host, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET kind = excluded.kind, device = excluded.device, caps = excluded.caps,
			on_host = excluded.on_host, last_seen = excluded.last_seen`,
		c.ID, c.Kind, c.Device, strings.Join(c.Caps, ","), onHost, now, now)
	return err
}

// SetPresence records how a client is being used, and when it's active,
// that it was just now.
func (s *Store) SetPresence(ctx context.Context, id, presence, focus string, active bool) error {
	now := ms(s.now().UTC())
	q := `UPDATE clients SET presence = ?, focus = ?, last_seen = ? WHERE id = ?`
	args := []any{presence, focus, now, id}
	if active {
		q = `UPDATE clients SET presence = ?, focus = ?, last_seen = ?, last_active = ? WHERE id = ?`
		args = []any{presence, focus, now, now, id}
	}
	res, err := s.q.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchClient records that a client was just connected.
func (s *Store) TouchClient(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE clients SET last_seen = ? WHERE id = ?`, ms(s.now().UTC()), id)
	return err
}

// ForgetClients removes the clients not seen since before, and returns their
// ids.
func (s *Store) ForgetClients(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.q.QueryContext(ctx, `DELETE FROM clients WHERE last_seen < ? RETURNING id`, ms(before.UTC()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
