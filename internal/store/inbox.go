package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

const inboxCols = `id, session_id, kind, text, options, multipart, at, read, state, handled_at`

func scanInboxItem(row interface{ Scan(...any) error }) (core.InboxItem, error) {
	var it core.InboxItem
	var options string
	var multipart, read int
	var at, handled int64
	err := row.Scan(&it.ID, &it.SessionID, &it.Kind, &it.Text, &options, &multipart, &at, &read, &it.State, &handled)
	if err != nil {
		return it, err
	}
	if err := json.Unmarshal([]byte(options), &it.Options); err != nil {
		return it, err
	}
	it.Multipart, it.Read, it.At = multipart != 0, read != 0, fromMS(at)
	if handled != 0 {
		it.HandledAt = fromMS(handled)
	}
	return it, nil
}

// AddInboxItem adds it to the back of the queue. The queue shows a session's latest
// item only, so anything still open for that session is no longer relevant.
func (s *Store) AddInboxItem(ctx context.Context, it core.InboxItem) error {
	options, err := json.Marshal(it.Options)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE inbox_items SET state = ?, handled_at = ? WHERE session_id = ? AND state = ?`,
		core.InboxStale, ms(s.now()), it.SessionID, core.InboxOpen); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO inbox_items (session_id, kind, text, options, multipart, at)
		VALUES (?,?,?,?,?,?)`, it.SessionID, it.Kind, it.Text, string(options), b2i(it.Multipart), ms(it.At)); err != nil {
		return err
	}
	return tx.Commit()
}

// CloseInboxItems marks a session's open items as no longer relevant.
func (s *Store) CloseInboxItems(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE inbox_items SET state = ?, handled_at = ? WHERE session_id = ? AND state = ?`,
		core.InboxStale, ms(s.now()), sessionID, core.InboxOpen)
	return err
}

// CloseUnanswerableItems marks questions and permissions of stopped sessions as no
// longer relevant: nothing is waiting for the answer. It reports whether any changed.
func (s *Store) CloseUnanswerableItems(ctx context.Context) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE inbox_items SET state = ?, handled_at = ?
		WHERE state = ? AND kind IN (?, ?) AND session_id IN (SELECT id FROM sessions WHERE status IN (?, ?))`,
		core.InboxStale, ms(s.now()), core.InboxOpen, core.InboxQuestion, core.InboxPermission,
		core.StatusPaused, core.StatusArchived)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// InboxItem returns one item.
func (s *Store) InboxItem(ctx context.Context, id int64) (*core.InboxItem, error) {
	it, err := scanInboxItem(s.db.QueryRowContext(ctx, `SELECT `+inboxCols+` FROM inbox_items WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// InboxQueue is the items still to deal with, in arrival order.
func (s *Store) InboxQueue(ctx context.Context) ([]core.InboxItem, error) {
	return s.inboxItems(ctx, `SELECT `+inboxCols+` FROM inbox_items WHERE state = ? ORDER BY id`, core.InboxOpen)
}

// InboxHistory is the newest limit items, of one session or (with "") of all.
func (s *Store) InboxHistory(ctx context.Context, sessionID string, limit int) ([]core.InboxItem, error) {
	if sessionID == "" {
		return s.inboxItems(ctx, `SELECT `+inboxCols+` FROM inbox_items ORDER BY id DESC LIMIT ?`, limit)
	}
	return s.inboxItems(ctx, `SELECT `+inboxCols+` FROM inbox_items WHERE session_id = ? ORDER BY id DESC LIMIT ?`,
		sessionID, limit)
}

func (s *Store) inboxItems(ctx context.Context, query string, args ...any) ([]core.InboxItem, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.InboxItem
	for rows.Next() {
		it, err := scanInboxItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetInboxRead marks items read or unread.
func (s *Store) SetInboxRead(ctx context.Context, ids []int64, read bool) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE inbox_items SET read = ? WHERE id = ?`, b2i(read), id); err != nil {
			return err
		}
	}
	return nil
}

// HandleInboxItem takes an open item out of the queue, as responded or dismissed. An
// item already handled keeps what happened to it.
func (s *Store) HandleInboxItem(ctx context.Context, id int64, state string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE inbox_items SET state = ?, read = 1, handled_at = ? WHERE id = ? AND state = ?`,
		state, ms(s.now()), id, core.InboxOpen)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneInbox deletes items handled before the given time. Open items stay.
func (s *Store) PruneInbox(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM inbox_items WHERE state <> ? AND handled_at < ?`, core.InboxOpen, ms(before))
	return err
}
