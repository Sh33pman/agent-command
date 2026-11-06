package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/brettmostert/agent-command/internal/core"
)

// ViewState returns the shared view state, with defaults if never saved.
func (s *Store) ViewState(ctx context.Context) (core.ViewState, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM view_state WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ViewState{Mode: "groups", Sort: "created"}, nil
	}
	if err != nil {
		return core.ViewState{}, err
	}
	var vs core.ViewState
	if err := json.Unmarshal([]byte(raw), &vs); err != nil {
		return core.ViewState{}, err
	}
	return vs, nil
}

// PutViewState saves the shared view state.
func (s *Store) PutViewState(ctx context.Context, vs core.ViewState) error {
	b, err := json.Marshal(vs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO view_state (id, state) VALUES (1, ?)
		ON CONFLICT (id) DO UPDATE SET state = excluded.state`, string(b))
	return err
}

// AppState returns a value the app stored ("" if there is none).
func (s *Store) AppState(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetAppState stores a value for the app.
func (s *Store) SetAppState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
