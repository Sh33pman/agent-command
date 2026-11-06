// Package store persists acmd's state in SQLite (SPEC §4.3): sessions,
// groups, the shared view state and a capped event log.
//
// Only the daemon writes; the store is safe for concurrent use but uses a
// single connection, so statements are serialized.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DefaultGroupID is the id of the Default group, created by the first migration.
const DefaultGroupID = "default"

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")

// ErrSchemaTooNew means the database was written by a newer acmd.
var ErrSchemaTooNew = errors.New("state database is newer than this acmd; upgrade acmd")

// Store is an open state database.
type Store struct {
	db   *sql.DB
	path string
	now  func() time.Time
}

// Option configures Open.
type Option func(*Store)

// WithClock overrides the clock (tests).
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// Open opens (creating if needed) the database at path and applies pending
// migrations. Before migrating an existing database it copies it to
// "<path>.bak-v<from>-<unix>". A database newer than this binary is refused
// (no silent downgrade, SPEC §4.3).
func Open(ctx context.Context, dbPath string, opts ...Option) (*Store, error) {
	s := &Store{path: dbPath, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	existed := fileExists(dbPath)

	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s.db = db
	if existed {
		_ = os.Chmod(dbPath, 0o600)
	}
	if err := s.migrate(ctx, existed); err != nil {
		_ = db.Close()
		return nil, err
	}
	if !existed {
		_ = os.Chmod(dbPath, 0o600)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("bad migration file name %q", e.Name())
		}
		b, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1..n without gaps, got %s", m.name)
		}
	}
	return ms, nil
}

// migrations is swappable in tests.
var migrations = loadMigrations

// SchemaVersion is the newest schema version this binary knows.
func SchemaVersion() int {
	ms, err := migrations()
	if err != nil {
		return 0
	}
	return len(ms)
}

func (s *Store) migrate(ctx context.Context, existed bool) error {
	ms, err := migrations()
	if err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(ms) {
		return fmt.Errorf("%w (database schema %d, acmd knows %d)", ErrSchemaTooNew, current, len(ms))
	}
	if current == len(ms) {
		return nil
	}
	if existed && current > 0 {
		if err := s.backup(ctx, current); err != nil {
			return fmt.Errorf("back up before migrating: %w", err)
		}
	}
	for _, m := range ms[current:] {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// backup copies the database (after a WAL checkpoint) next to itself.
func (s *Store) backup(ctx context.Context, from int) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	dst := fmt.Sprintf("%s.bak-v%d-%d", s.path, from, s.now().Unix())
	return copyFile(s.path, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Clean(dst), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// --- ids and times ---------------------------------------------------------

var (
	idMu      sync.Mutex
	idEntropy = ulid.Monotonic(rand.Reader, 0)
)

// NewID returns a new ULID (lexicographically sortable by creation time).
func NewID() string {
	idMu.Lock()
	defer idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), idEntropy).String()
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
