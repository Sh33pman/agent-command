package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/brettmostert/agent-command/internal/core"
)

// runsKept is how many runs of each job are kept.
const runsKept = 100

const jobCols = `id, spec, cron, once_at, paused, created_at, updated_at, last_due`

// SaveJob adds a job (no ID) or replaces its spec and schedule. A new or
// rescheduled job starts counting from now, so it doesn't run for times
// before it was set.
func (s *Store) SaveJob(ctx context.Context, j *core.Job) error {
	spec, err := json.Marshal(j.JobSpec)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	j.UpdatedAt = now
	if j.ID == "" {
		j.ID, j.CreatedAt, j.LastDue = NewID(), now, now
		_, err = s.db.ExecContext(ctx, `INSERT INTO jobs (`+jobCols+`) VALUES (?,?,?,?,?,?,?,?)`,
			j.ID, string(spec), j.Cron, ms(j.Once), b2i(j.Paused), ms(j.CreatedAt), ms(j.UpdatedAt), ms(j.LastDue))
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		old, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, j.ID))
		if err != nil {
			return err
		}
		j.CreatedAt, j.LastDue = old.CreatedAt, old.LastDue
		if old.Cron != j.Cron || !old.Once.Equal(j.Once) {
			j.LastDue = now
		}
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET spec = ?, cron = ?, once_at = ?, paused = ?, updated_at = ?, last_due = ? WHERE id = ?`,
			string(spec), j.Cron, ms(j.Once), b2i(j.Paused), ms(j.UpdatedAt), ms(j.LastDue), j.ID)
		return err
	})
}

// Job returns a job, with its last run.
func (s *Store) Job(ctx context.Context, id string) (*core.Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	runs, err := s.JobRuns(ctx, id, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		j.LastRun = &runs[0]
	}
	return j, nil
}

// Jobs lists the jobs by name, each with its last run.
func (s *Store) Jobs(ctx context.Context) ([]*core.Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs`)
	if err != nil {
		return nil, err
	}
	var jobs []*core.Job
	byID := map[string]*core.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		jobs = append(jobs, j)
		byID[j.ID] = j
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	last, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM job_runs
		WHERE id IN (SELECT max(id) FROM job_runs GROUP BY job_id)`)
	if err != nil {
		return nil, err
	}
	defer last.Close()
	for last.Next() {
		r, err := scanRun(last)
		if err != nil {
			return nil, err
		}
		if j := byID[r.JobID]; j != nil {
			j.LastRun = &r
		}
	}
	sortJobs(jobs)
	return jobs, last.Err()
}

// SetJobPaused pauses or resumes a job. Resuming counts from now, so the
// runs it was paused through don't show as missed.
func (s *Store) SetJobPaused(ctx context.Context, id string, paused bool) error {
	now := ms(s.now())
	q := `UPDATE jobs SET paused = ?, updated_at = ? WHERE id = ?`
	args := []any{b2i(paused), now, id}
	if !paused {
		q = `UPDATE jobs SET paused = 0, updated_at = ?, last_due = max(last_due, ?) WHERE id = ?`
		args = []any{now, now, id}
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	return notFoundIfNone(res, err)
}

// DeleteJob deletes a job and its runs. The sessions its runs started stay.
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	return notFoundIfNone(res, err)
}

// RecordRun adds a run, moves the job's LastDue on to r.Due, and keeps only the
// latest runsKept runs.
func (s *Store) RecordRun(ctx context.Context, r *core.JobRun) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if r.At.IsZero() {
			r.At = s.now().UTC()
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO job_runs (job_id, due, at, result, session_id, detail) VALUES (?,?,?,?,?,?)`,
			r.JobID, ms(r.Due), ms(r.At), r.Result, r.SessionID, r.Detail)
		if err != nil {
			return err
		}
		if r.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET last_due = max(last_due, ?) WHERE id = ?`, ms(r.Due), r.JobID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM job_runs WHERE job_id = ? AND id NOT IN
			(SELECT id FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?)`, r.JobID, r.JobID, runsKept)
		return err
	})
}

// JobRuns lists a job's runs, newest first (limit 0: all that are kept).
func (s *Store) JobRuns(ctx context.Context, jobID string, limit int) ([]core.JobRun, error) {
	if limit <= 0 {
		limit = runsKept
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM job_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.JobRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const runCols = `id, job_id, due, at, result, session_id, detail`

func scanRun(row scanner) (core.JobRun, error) {
	var r core.JobRun
	var due, at int64
	err := row.Scan(&r.ID, &r.JobID, &due, &at, &r.Result, &r.SessionID, &r.Detail)
	r.Due, r.At = fromMS(due), fromMS(at)
	return r, err
}

func scanJob(row scanner) (*core.Job, error) {
	var (
		j                               core.Job
		spec                            string
		once, created, updated, lastDue int64
		paused                          int
	)
	err := row.Scan(&j.ID, &spec, &j.Cron, &once, &paused, &created, &updated, &lastDue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(spec), &j.JobSpec); err != nil {
		return nil, err
	}
	j.Once, j.Paused = fromMS(once), paused != 0
	j.CreatedAt, j.UpdatedAt, j.LastDue = fromMS(created), fromMS(updated), fromMS(lastDue)
	return &j, nil
}

func notFoundIfNone(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// sortJobs orders jobs by name, then by when they were made.
func sortJobs(jobs []*core.Job) {
	sort.SliceStable(jobs, func(a, b int) bool {
		if jobs[a].Name != jobs[b].Name {
			return jobs[a].Name < jobs[b].Name
		}
		return jobs[a].CreatedAt.Before(jobs[b].CreatedAt)
	})
}
