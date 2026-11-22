package store

import (
	"context"
	"time"
)

// MaxEvents caps the event log.
const MaxEvents = 10000

// LoggedEvent is one row of the event log. It deliberately has no prompt,
// tool input or transcript content.
type LoggedEvent struct {
	ID              int64
	At              time.Time
	SessionID       string
	ClaudeSessionID string
	Kind            string
	Detail          string
	ToolName        string
	// Caller is who made a change (an op's), as the daemon describes them.
	Caller string
}

// AppendEvent logs an event and keeps the log at MaxEvents rows.
func (s *Store) AppendEvent(ctx context.Context, e LoggedEvent) error {
	if e.At.IsZero() {
		e.At = s.now()
	}
	res, err := s.q.ExecContext(ctx, `INSERT INTO events (at, session_id, claude_session_id, kind, detail, tool_name, caller)
		VALUES (?,?,?,?,?,?,?)`, ms(e.At), e.SessionID, e.ClaudeSessionID, e.Kind, e.Detail, e.ToolName, e.Caller)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	// Prune in batches: only when we're 10% over the cap.
	if id%(MaxEvents/10) == 0 {
		_, err = s.q.ExecContext(ctx, `DELETE FROM events WHERE id <= ?`, id-MaxEvents)
	}
	return err
}

// RecentEvents returns up to n events, newest first; sessionID filters when set.
func (s *Store) RecentEvents(ctx context.Context, sessionID string, n int) ([]LoggedEvent, error) {
	q := `SELECT id, at, session_id, claude_session_id, kind, detail, tool_name, caller FROM events`
	args := []any{}
	if sessionID != "" {
		q += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	rows, err := s.q.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, n)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoggedEvent
	for rows.Next() {
		var e LoggedEvent
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.SessionID, &e.ClaudeSessionID, &e.Kind, &e.Detail, &e.ToolName, &e.Caller); err != nil {
			return nil, err
		}
		e.At = fromMS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventCount returns the number of logged events.
func (s *Store) EventCount(ctx context.Context) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT count(*) FROM events`).Scan(&n)
	return n, err
}
