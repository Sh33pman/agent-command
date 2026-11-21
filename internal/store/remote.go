package store

import (
	"context"
	"fmt"

	"github.com/brettmostert/agent-command/internal/config"
)

// Remote's data: the clients Hosting lets in, and the hosts this machine
// connects to. config.toml may list more, read-only; these are the ones ops
// manage.

// AuthorizedClients lists the authorized clients, oldest first.
func (s *Store) AuthorizedClients(ctx context.Context) ([]config.AuthorizedClient, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT name, fingerprint FROM remote_clients ORDER BY added_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []config.AuthorizedClient
	for rows.Next() {
		var c config.AuthorizedClient
		if err := rows.Scan(&c.Name, &c.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddAuthorizedClient adds a client, or renames the one with its
// fingerprint.
func (s *Store) AddAuthorizedClient(ctx context.Context, c config.AuthorizedClient) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO remote_clients (fingerprint, name, added_at) VALUES (?, ?, ?)
		ON CONFLICT (fingerprint) DO UPDATE SET name = excluded.name`, c.Fingerprint, c.Name, ms(s.now().UTC()))
	return err
}

// RemoveAuthorizedClient takes the client with this fingerprint off the
// list, and says whether it was there.
func (s *Store) RemoveAuthorizedClient(ctx context.Context, fingerprint string) (bool, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM remote_clients WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// KnownHosts lists the known hosts, oldest first.
func (s *Store) KnownHosts(ctx context.Context) ([]config.KnownHost, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT name, address, fingerprint, cert FROM remote_hosts ORDER BY added_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []config.KnownHost
	for rows.Next() {
		var h config.KnownHost
		if err := rows.Scan(&h.Name, &h.Address, &h.Fingerprint, &h.Cert); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SaveKnownHost adds h, or, with replace set, replaces the host of that
// name with it (keeping its place in the list).
func (s *Store) SaveKnownHost(ctx context.Context, replace string, h config.KnownHost) error {
	if replace == "" {
		_, err := s.q.ExecContext(ctx, `INSERT INTO remote_hosts (name, address, fingerprint, cert, added_at) VALUES (?, ?, ?, ?, ?)`,
			h.Name, h.Address, h.Fingerprint, h.Cert, ms(s.now().UTC()))
		return err
	}
	res, err := s.q.ExecContext(ctx, `UPDATE remote_hosts SET name = ?, address = ?, fingerprint = ?, cert = ? WHERE name = ?`,
		h.Name, h.Address, h.Fingerprint, h.Cert, replace)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("known host %s: %w", replace, ErrNotFound)
	}
	return nil
}

// RemoveKnownHost forgets the host of that name, and says whether it was
// there.
func (s *Store) RemoveKnownHost(ctx context.Context, name string) (bool, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM remote_hosts WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
