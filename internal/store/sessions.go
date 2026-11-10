package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
)

const sessionCols = `id, name, description, group_id, name_source, cwd, claude_session_id,
	claude_history, transcript_path, status, status_detail, unseen, kind, bg_id, pid,
	external, created_at, last_event_at, archived_at, harness`

// CreateSession inserts a new session. An empty ID gets a new ULID, an empty
// GroupID the Default group, and a zero CreatedAt the current time.
func (s *Store) CreateSession(ctx context.Context, sess *core.Session) error {
	if sess.ID == "" {
		sess.ID = NewID()
	}
	if sess.GroupID == "" {
		sess.GroupID = DefaultGroupID
	}
	if sess.NameSource == "" {
		sess.NameSource = core.NameAuto
	}
	if sess.Kind == "" {
		sess.Kind = core.KindBackground
	}
	if sess.Harness == "" {
		sess.Harness = core.HarnessClaude
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = s.now().UTC()
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		args, err := sessionArgs(sess)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (`+sessionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		return syncClaudeIDs(ctx, tx, sess)
	})
}

// UpdateSession writes all fields of an existing session.
func (s *Store) UpdateSession(ctx context.Context, sess *core.Session) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		args, err := sessionArgs(sess)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET name=?, description=?, group_id=?, name_source=?, cwd=?,
			claude_session_id=?, claude_history=?, transcript_path=?, status=?, status_detail=?, unseen=?, kind=?,
			bg_id=?, pid=?, external=?, created_at=?, last_event_at=?, archived_at=?, harness=? WHERE id=?`,
			append(args[1:], args[0])...)
		if err != nil {
			return fmt.Errorf("update session: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return syncClaudeIDs(ctx, tx, sess)
	})
}

// Session returns the session with the given acmd id.
func (s *Store) Session(ctx context.Context, id string) (*core.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
}

// SessionByClaudeID finds the session that has (or had) this Claude session
// id, so hooks from a /clear'ed or forked session still land on it.
func (s *Store) SessionByClaudeID(ctx context.Context, claudeID string) (*core.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+prefixed("s", sessionCols)+`
		FROM claude_ids c JOIN sessions s ON s.id = c.session_id WHERE c.claude_session_id = ?`, claudeID))
}

// SessionByBgID finds a session acmd started (`claude --bg`) that hasn't
// been linked to its Claude session id yet. Claude's short id is the first
// 8 characters of the session id (M0 A3), which links the first hook.
func (s *Store) SessionByBgID(ctx context.Context, bgID string) (*core.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE bg_id = ? AND claude_session_id = '' ORDER BY created_at DESC LIMIT 1`, bgID))
}

// SessionByClaudePrefix finds the session whose Claude session id starts
// with prefix (a short id), e.g. one its first hook created.
func (s *Store) SessionByClaudePrefix(ctx context.Context, prefix string) (*core.Session, error) {
	if prefix == "" {
		return nil, ErrNotFound
	}
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+prefixed("s", sessionCols)+`
		FROM claude_ids c JOIN sessions s ON s.id = c.session_id
		WHERE substr(c.claude_session_id, 1, length(?)) = ? ORDER BY s.created_at DESC LIMIT 1`, prefix, prefix))
}

// Sessions lists sessions in creation order; archived ones only if includeArchived.
func (s *Store) Sessions(ctx context.Context, includeArchived bool) ([]*core.Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions`
	if !includeArchived {
		q += ` WHERE archived_at = 0`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// DeleteSession permanently removes a session (`acmd purge`).
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MoveSessions puts sessions into a group (exactly one group each).
func (s *Store) MoveSessions(ctx context.Context, groupID string, ids ...string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := liveGroupExists(ctx, tx, groupID); err != nil {
			return err
		}
		for _, id := range ids {
			res, err := tx.ExecContext(ctx, `UPDATE sessions SET group_id = ? WHERE id = ?`, groupID, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("session %s: %w", id, ErrNotFound)
			}
		}
		return nil
	})
}

func syncClaudeIDs(ctx context.Context, tx *sql.Tx, sess *core.Session) error {
	// An agent in a terminal visits it. Its conversation stays the user's to
	// resume, or the row's that already holds it.
	if sess.IsTerminal() {
		return nil
	}
	ids := append([]string{}, sess.ClaudeHistory...)
	if sess.ClaudeSessionID != "" {
		ids = append(ids, sess.ClaudeSessionID)
	}
	for _, cid := range ids {
		if cid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO claude_ids (claude_session_id, session_id) VALUES (?, ?)
			ON CONFLICT (claude_session_id) DO UPDATE SET session_id = excluded.session_id`, cid, sess.ID); err != nil {
			return err
		}
	}
	return nil
}

func sessionArgs(sess *core.Session) ([]any, error) {
	hist, err := json.Marshal(nonNil(sess.ClaudeHistory))
	if err != nil {
		return nil, err
	}
	ext := ""
	if sess.External != nil {
		b, err := json.Marshal(sess.External)
		if err != nil {
			return nil, err
		}
		ext = string(b)
	}
	return []any{sess.ID, sess.Name, sess.Description, sess.GroupID, string(sess.NameSource), sess.Cwd,
		sess.ClaudeSessionID, string(hist), sess.TranscriptPath, string(sess.Status), sess.StatusDetail,
		b2i(sess.Unseen), string(sess.Kind), sess.BgID, sess.Pid, ext,
		ms(sess.CreatedAt), ms(sess.LastEventAt), ms(sess.ArchivedAt), sess.Harness}, nil
}

type scanner interface{ Scan(...any) error }

func scanSession(row scanner) (*core.Session, error) {
	var (
		sess                         core.Session
		nameSource, status, kind     string
		hist, ext                    string
		unseen                       int
		created, lastEvent, archived int64
	)
	err := row.Scan(&sess.ID, &sess.Name, &sess.Description, &sess.GroupID, &nameSource, &sess.Cwd,
		&sess.ClaudeSessionID, &hist, &sess.TranscriptPath, &status, &sess.StatusDetail, &unseen, &kind,
		&sess.BgID, &sess.Pid, &ext, &created, &lastEvent, &archived, &sess.Harness)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.NameSource, sess.Status, sess.Kind = core.NameSource(nameSource), core.Status(status), core.Kind(kind)
	sess.Unseen = unseen != 0
	sess.CreatedAt, sess.LastEventAt, sess.ArchivedAt = fromMS(created), fromMS(lastEvent), fromMS(archived)
	if err := json.Unmarshal([]byte(hist), &sess.ClaudeHistory); err != nil {
		return nil, fmt.Errorf("session %s claude_history: %w", sess.ID, err)
	}
	if len(sess.ClaudeHistory) == 0 {
		sess.ClaudeHistory = nil
	}
	if ext != "" {
		sess.External = &core.PaneLoc{}
		if err := json.Unmarshal([]byte(ext), sess.External); err != nil {
			return nil, fmt.Errorf("session %s external: %w", sess.ID, err)
		}
	}
	return &sess, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// prefixed qualifies a comma-separated column list with a table alias.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
