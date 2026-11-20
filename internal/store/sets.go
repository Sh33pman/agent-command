package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brettmostert/agent-command/internal/core"
)

// Errors for set rules.
var (
	ErrSetTooSmall = errors.New("a set needs at least two sessions")
	ErrInSet       = errors.New("that session is already in a set")
	ErrSetHosts    = errors.New("a set's sessions must all be on one host")
	ErrArchived    = errors.New("that session is archived")
)

// The rules, enforced here in every change: a session is in one set at
// most; a set has at least two live sessions, all in one group and on one
// host; a set with one left ends. Archiving or deleting a session takes it
// out of its set.

// CreateSet makes a set of ids, in that order. An empty id gets a new one.
// The sessions move to the first one's group.
func (s *Store) CreateSet(ctx context.Context, id string, ids ...string) (*core.Set, error) {
	if id == "" {
		id = NewID()
	}
	var set *core.Set
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		members, err := setCandidates(ctx, tx, ids)
		if err != nil {
			return err
		}
		if len(members) < 2 {
			return ErrSetTooSmall
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sets WHERE id = ?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return fmt.Errorf("set %s already exists", id)
		}
		now := s.now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO sets (id, created_at) VALUES (?, ?)`, id, ms(now)); err != nil {
			return err
		}
		group := members[0].GroupID
		for i, m := range members {
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET set_id = ?, set_order = ?, group_id = ? WHERE id = ?`,
				id, i+1, group, m.ID); err != nil {
				return err
			}
		}
		set = &core.Set{ID: id, GroupID: group, Host: members[0].Host, CreatedAt: now}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return set, nil
}

// AddToSet puts session id in set setID, right after member after ("": at
// the end). It moves to the set's group.
func (s *Store) AddToSet(ctx context.Context, setID, id, after string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		members, err := setMembers(ctx, tx, setID)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			return fmt.Errorf("set %s: %w", setID, ErrNotFound)
		}
		cand, err := setCandidates(ctx, tx, []string{id})
		if err != nil {
			return err
		}
		sess := cand[0]
		if sess.Host != members[0].Host {
			return ErrSetHosts
		}
		at := len(members) // insert after this many members
		if after != "" {
			at = -1
			for i, m := range members {
				if m.ID == after {
					at = i + 1
				}
			}
			if at < 0 {
				return fmt.Errorf("session %s isn't in set %s", after, setID)
			}
		}
		order := make([]string, 0, len(members)+1)
		for _, m := range members[:at] {
			order = append(order, m.ID)
		}
		order = append(order, sess.ID)
		for _, m := range members[at:] {
			order = append(order, m.ID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET set_id = ?, group_id = ? WHERE id = ?`,
			setID, members[0].GroupID, sess.ID); err != nil {
			return err
		}
		return renumber(ctx, tx, order)
	})
}

// RemoveFromSet takes session id out of its set, which ends if one is left.
// A session in no set is left as it is.
func (s *Store) RemoveFromSet(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		setID, err := sessionSet(ctx, tx, id)
		if err != nil || setID == "" {
			return err
		}
		return leaveSet(ctx, tx, id, setID)
	})
}

// EndSet ends a set. Its sessions stay where they are, on their own.
func (s *Store) EndSet(ctx context.Context, setID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sets WHERE id = ?`, setID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("set %s: %w", setID, ErrNotFound)
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET set_id = '', set_order = 0 WHERE set_id = ?`, setID)
		return err
	})
}

// Set returns a set by id.
func (s *Store) Set(ctx context.Context, id string) (*core.Set, error) {
	sets, err := s.sets(ctx, `WHERE st.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(sets) == 0 {
		return nil, fmt.Errorf("set %s: %w", id, ErrNotFound)
	}
	return sets[0], nil
}

// Sets lists every set, oldest first.
func (s *Store) Sets(ctx context.Context) ([]*core.Set, error) { return s.sets(ctx, "") }

func (s *Store) sets(ctx context.Context, where string, args ...any) ([]*core.Set, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT st.id, st.created_at,
		(SELECT group_id FROM sessions WHERE set_id = st.id ORDER BY set_order LIMIT 1),
		(SELECT host FROM sessions WHERE set_id = st.id ORDER BY set_order LIMIT 1)
		FROM sets st `+where+` ORDER BY st.created_at, st.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Set
	for rows.Next() {
		var (
			set         core.Set
			created     int64
			group, host sql.NullString
		)
		if err := rows.Scan(&set.ID, &created, &group, &host); err != nil {
			return nil, err
		}
		set.CreatedAt, set.GroupID, set.Host = fromMS(created), group.String, host.String
		out = append(out, &set)
	}
	return out, rows.Err()
}

// setCandidates reads sessions that may join a set: they exist, are live,
// are in no set, and are all on one host. Repeated ids count once.
func setCandidates(ctx context.Context, tx *sql.Tx, ids []string) ([]*core.Session, error) {
	seen := map[string]bool{}
	var out []*core.Session
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		sess, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionReadCols+` FROM sessions WHERE id = ?`, id))
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", id, err)
		}
		switch {
		case sess.ArchivedAt.Unix() > 0 || sess.Status == core.StatusArchived:
			return nil, fmt.Errorf("session %s: %w", id, ErrArchived)
		case sess.SetID != "":
			return nil, fmt.Errorf("session %s: %w", id, ErrInSet)
		case len(out) > 0 && sess.Host != out[0].Host:
			return nil, ErrSetHosts
		}
		out = append(out, sess)
	}
	return out, nil
}

// setMembers lists a set's sessions in order.
func setMembers(ctx context.Context, tx *sql.Tx, setID string) ([]*core.Session, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+sessionReadCols+` FROM sessions WHERE set_id = ? ORDER BY set_order, id`, setID)
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

func sessionSet(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var setID string
	err := tx.QueryRowContext(ctx, `SELECT set_id FROM sessions WHERE id = ?`, id).Scan(&setID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("session %s: %w", id, ErrNotFound)
	}
	return setID, err
}

// leaveSet takes id out of setID, then settles the set.
func leaveSet(ctx context.Context, tx *sql.Tx, id, setID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET set_id = '', set_order = 0 WHERE id = ?`, id); err != nil {
		return err
	}
	return settleSet(ctx, tx, setID)
}

// settleSet ends a set with fewer than two sessions left, and otherwise
// numbers its members 1..n again.
func settleSet(ctx context.Context, tx *sql.Tx, setID string) error {
	members, err := setMembers(ctx, tx, setID)
	if err != nil {
		return err
	}
	if len(members) >= 2 {
		ids := make([]string, len(members))
		for i, m := range members {
			ids[i] = m.ID
		}
		return renumber(ctx, tx, ids)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET set_id = '', set_order = 0 WHERE set_id = ?`, setID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM sets WHERE id = ?`, setID)
	return err
}

func renumber(ctx context.Context, tx *sql.Tx, ids []string) error {
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET set_order = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return nil
}

// withSets adds, to ids, every other member of the sets they're in, so a
// move takes whole sets.
func withSets(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range ids {
		add(id)
		setID, err := sessionSet(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if setID == "" {
			continue
		}
		members, err := setMembers(ctx, tx, setID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			add(m.ID)
		}
	}
	return out, nil
}
