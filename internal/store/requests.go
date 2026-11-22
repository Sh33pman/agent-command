package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Request is the result of a creating op that carried a request id.
type Request struct {
	ID, Op string
	Result []byte // the op's result, as JSON
	At     time.Time
}

// Request is the result kept for id, made since since. ErrNotFound when
// there's none, or it's older.
func (s *Store) Request(ctx context.Context, id string, since time.Time) (Request, error) {
	r := Request{ID: id}
	var at int64
	var result string
	err := s.q.QueryRowContext(ctx, `SELECT op, result, created_at FROM requests WHERE id = ? AND created_at >= ?`,
		id, ms(since.UTC())).Scan(&r.Op, &result, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.Result, r.At = []byte(result), time.UnixMilli(at).UTC()
	return r, err
}

// SaveRequest keeps r, replacing one of the same id, and forgets the
// results made before before.
func (s *Store) SaveRequest(ctx context.Context, r Request, before time.Time) error {
	if _, err := s.q.ExecContext(ctx, `DELETE FROM requests WHERE created_at < ?`, ms(before.UTC())); err != nil {
		return err
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO requests (id, op, result, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET op = excluded.op, result = excluded.result, created_at = excluded.created_at`,
		r.ID, r.Op, string(r.Result), ms(r.At.UTC()))
	return err
}
