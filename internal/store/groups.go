package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
)

// Errors for group rules (SPEC §8.4).
var (
	ErrDefaultGroup  = errors.New("the Default group can't be renamed, deleted or archived")
	ErrDuplicateName = errors.New("a group with that name already exists")
	ErrEmptyName     = errors.New("group name is empty")
)

// Palette is the round-robin color order for new groups (SPEC §8.4).
var Palette = []string{"blue", "magenta", "cyan", "yellow", "green", "red"}

const groupCols = `id, name, color, sort_key, collapsed, is_default, created_at, archived_at`

// CreateGroup adds a group. An empty color is assigned from Palette; new
// groups sort after existing ones but before Default.
func (s *Store) CreateGroup(ctx context.Context, name, color string) (*core.Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrEmptyName
	}
	g := &core.Group{ID: NewID(), Name: name, Color: color, CreatedAt: s.now().UTC()}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var count, maxKey int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(max(sort_key), 0) FROM groups WHERE is_default = 0`).Scan(&count, &maxKey); err != nil {
			return err
		}
		if g.Color == "" {
			g.Color = Palette[count%len(Palette)]
		}
		g.SortKey = maxKey + 10
		_, err := tx.ExecContext(ctx, `INSERT INTO groups (`+groupCols+`) VALUES (?,?,?,?,?,?,?,?)`,
			g.ID, g.Name, g.Color, g.SortKey, 0, 0, ms(g.CreatedAt), 0)
		return mapUnique(err)
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// Group returns a group by id.
func (s *Store) Group(ctx context.Context, id string) (*core.Group, error) {
	return scanGroup(s.db.QueryRowContext(ctx, `SELECT `+groupCols+` FROM groups WHERE id = ?`, id))
}

// GroupByName finds a non-archived group by name, case-insensitively.
func (s *Store) GroupByName(ctx context.Context, name string) (*core.Group, error) {
	return scanGroup(s.db.QueryRowContext(ctx, `SELECT `+groupCols+` FROM groups
		WHERE lower(name) = lower(?) AND archived_at = 0`, strings.TrimSpace(name)))
}

// Groups lists groups in display order (sort_key; Default last).
func (s *Store) Groups(ctx context.Context, includeArchived bool) ([]*core.Group, error) {
	q := `SELECT ` + groupCols + ` FROM groups`
	if !includeArchived {
		q += ` WHERE archived_at = 0`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY is_default, sort_key, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RenameGroup renames a non-default group.
func (s *Store) RenameGroup(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEmptyName
	}
	return s.updateNonDefault(ctx, id, `UPDATE groups SET name = ? WHERE id = ?`, name, id)
}

// SetGroupColor changes a group's color.
func (s *Store) SetGroupColor(ctx context.Context, id, color string) error {
	return s.exec1(ctx, `UPDATE groups SET color = ? WHERE id = ?`, color, id)
}

// SetGroupCollapsed records a group's collapsed state.
func (s *Store) SetGroupCollapsed(ctx context.Context, id string, collapsed bool) error {
	return s.exec1(ctx, `UPDATE groups SET collapsed = ? WHERE id = ?`, b2i(collapsed), id)
}

// SetGroupSortKey moves a group in the display order.
func (s *Store) SetGroupSortKey(ctx context.Context, id string, key int) error {
	return s.updateNonDefault(ctx, id, `UPDATE groups SET sort_key = ? WHERE id = ?`, key, id)
}

// DeleteGroup removes a non-default group, moving its sessions to Default
// first (SPEC §6.5: deleting never archives anything).
func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkNonDefault(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET group_id = ? WHERE group_id = ?`, DefaultGroupID, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
		return err
	})
}

// SetGroupArchived archives or unarchives a non-default group. Archiving
// the group's sessions is the caller's job (it involves `claude stop`).
func (s *Store) SetGroupArchived(ctx context.Context, id string, archived bool) error {
	at := int64(0)
	if archived {
		at = ms(s.now())
	}
	return s.updateNonDefault(ctx, id, `UPDATE groups SET archived_at = ? WHERE id = ?`, at, id)
}

func (s *Store) updateNonDefault(ctx context.Context, id, q string, args ...any) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkNonDefault(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, q, args...)
		return mapUnique(err)
	})
}

func (s *Store) exec1(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func checkNonDefault(ctx context.Context, tx *sql.Tx, id string) error {
	var isDefault int
	err := tx.QueryRowContext(ctx, `SELECT is_default FROM groups WHERE id = ?`, id).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if isDefault == 1 {
		return ErrDefaultGroup
	}
	return nil
}

func liveGroupExists(ctx context.Context, tx *sql.Tx, id string) error {
	var archived int64
	err := tx.QueryRowContext(ctx, `SELECT archived_at FROM groups WHERE id = ?`, id).Scan(&archived)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("group %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if archived != 0 {
		return fmt.Errorf("group %s is archived", id)
	}
	return nil
}

func mapUnique(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicateName
	}
	return err
}

func scanGroup(row scanner) (*core.Group, error) {
	var (
		g                   core.Group
		collapsed, isDef    int
		created, archivedAt int64
	)
	err := row.Scan(&g.ID, &g.Name, &g.Color, &g.SortKey, &collapsed, &isDef, &created, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Collapsed, g.IsDefault = collapsed != 0, isDef != 0
	g.CreatedAt, g.ArchivedAt = fromMS(created), fromMS(archivedAt)
	return &g, nil
}
