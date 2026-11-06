package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/brettmostert/agent-command/internal/core"
)

// PutView inserts or replaces a view. An empty ID gets a new ULID.
func (s *Store) PutView(ctx context.Context, v *core.View) error {
	if v.ID == "" {
		v.ID = NewID()
	}
	pane, err := json.Marshal(v.Pane)
	if err != nil {
		return err
	}
	side, err := json.Marshal(v.Sidebar)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO views (id, pane, sidebar, selected, sidebar_hidden, last_focused_at)
		VALUES (?,?,?,?,?,?) ON CONFLICT (id) DO UPDATE SET pane=excluded.pane, sidebar=excluded.sidebar,
		selected=excluded.selected, sidebar_hidden=excluded.sidebar_hidden, last_focused_at=excluded.last_focused_at`,
		v.ID, string(pane), string(side), v.Selected, b2i(v.SidebarHidden), ms(v.LastFocusedAt))
	return err
}

// View returns a view by id.
func (s *Store) View(ctx context.Context, id string) (*core.View, error) {
	return scanView(s.db.QueryRowContext(ctx, `SELECT id, pane, sidebar, selected, sidebar_hidden, last_focused_at
		FROM views WHERE id = ?`, id))
}

// Views lists views, most recently focused first.
func (s *Store) Views(ctx context.Context) ([]*core.View, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, pane, sidebar, selected, sidebar_hidden, last_focused_at
		FROM views ORDER BY last_focused_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.View
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteView removes a view (its pane closed). Sessions are unaffected.
func (s *Store) DeleteView(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM views WHERE id = ?`, id)
}

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

func scanView(row scanner) (*core.View, error) {
	var (
		v           core.View
		pane, side  string
		hidden      int
		lastFocused int64
	)
	err := row.Scan(&v.ID, &pane, &side, &v.Selected, &hidden, &lastFocused)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(pane), &v.Pane); err != nil {
		return nil, err
	}
	if side != "" {
		if err := json.Unmarshal([]byte(side), &v.Sidebar); err != nil {
			return nil, err
		}
	}
	v.SidebarHidden, v.LastFocusedAt = hidden != 0, fromMS(lastFocused)
	return &v, nil
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
