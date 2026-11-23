package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
)

// ErrHarnessTaken is a harness name another harness has, ignoring case and
// "_" for a space.
var ErrHarnessTaken = errors.New("another harness has that name")

const harnessCols = `id, kind, name, position, command, args, config_dir, created_at`

// Harnesses lists the harnesses, in list order.
func (s *Store) Harnesses(ctx context.Context) ([]core.Harness, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+harnessCols+` FROM harnesses ORDER BY position, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Harness{}
	for rows.Next() {
		h, err := scanHarness(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// Harness returns one harness.
func (s *Store) Harness(ctx context.Context, id string) (*core.Harness, error) {
	return scanHarness(s.q.QueryRowContext(ctx, `SELECT `+harnessCols+` FROM harnesses WHERE id = ?`, id))
}

// SaveHarness adds h (no ID: it goes last) or replaces it. Its name must be
// one no other harness has, ignoring case and "_" for a space
// (ErrHarnessTaken); the caller checks the name's form.
func (s *Store) SaveHarness(ctx context.Context, h *core.Harness) error {
	args, err := json.Marshal(nonNil(h.Args))
	if err != nil {
		return err
	}
	h.Name = strings.TrimSpace(h.Name)
	slug := core.HarnessSlug(h.Name)
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var other string
		err := tx.QueryRowContext(ctx, `SELECT id FROM harnesses WHERE slug = ? AND id != ?`, slug, h.ID).Scan(&other)
		if err == nil {
			return ErrHarnessTaken
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if h.ID == "" {
			h.ID, h.CreatedAt = NewID(), s.now().UTC()
			if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(position), 0) + 1 FROM harnesses`).Scan(&h.Order); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO harnesses (`+harnessCols+`, slug) VALUES (?,?,?,?,?,?,?,?,?)`,
				h.ID, h.Kind, h.Name, h.Order, h.Command, string(args), h.ConfigDir, ms(h.CreatedAt), slug)
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE harnesses SET name = ?, slug = ?, command = ?, args = ?, config_dir = ? WHERE id = ?`,
			h.Name, slug, h.Command, string(args), h.ConfigDir, h.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RemoveHarness removes a harness. Its sessions keep its id, and show it as
// removed; the caller refuses while a job uses it.
func (s *Store) RemoveHarness(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM harnesses WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanHarness(row scanner) (*core.Harness, error) {
	var (
		h       core.Harness
		args    string
		created int64
	)
	err := row.Scan(&h.ID, &h.Kind, &h.Name, &h.Order, &h.Command, &args, &h.ConfigDir, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(args), &h.Args); err != nil {
		h.Args = nil
	}
	if len(h.Args) == 0 {
		h.Args = nil
	}
	h.CreatedAt = fromMS(created)
	return &h, nil
}
