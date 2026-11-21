package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

var ErrNoJob = errors.New("store: no job")

const jobCols = `id, kind, COALESCE(source_id, 0), target, priority_class, state, not_before, attempts, max_attempts,
	last_error, dedupe_key, created_at, started_at, finished_at`

// jobColsJ is jobCols qualified for queries joining sources.
const jobColsJ = `j.id, j.kind, COALESCE(j.source_id, 0), j.target, j.priority_class, j.state, j.not_before, j.attempts,
	j.max_attempts, j.last_error, j.dedupe_key, j.created_at, j.started_at, j.finished_at`

func scanJob(row interface{ Scan(...any) error }) (model.Job, error) {
	var j model.Job
	var nb, created, started, finished int64
	err := row.Scan(&j.ID, &j.Kind, &j.SourceID, &j.Target, &j.Priority, &j.State, &nb, &j.Attempts, &j.MaxAttempts,
		&j.LastError, &j.DedupeKey, &created, &started, &finished)
	j.NotBefore, j.CreatedAt, j.StartedAt, j.FinishedAt = unixTime(nb), unixTime(created), unixTime(started), unixTime(finished)
	return j, err
}

// DefaultMaxAttempts is how many attempts every job gets.
const DefaultMaxAttempts = 5

// NewJob is what EnqueueJob takes. A new job is due at once and gets DefaultMaxAttempts.
type NewJob struct {
	Kind      string
	SourceID  int64 // 0 = none
	Target    int64 // page number or id
	Priority  int   // class: lower runs first
	DedupeKey string
}

// EnqueueJob inserts a job unless an active (queued/running) job with the same dedupe key
// exists. If one exists with a lower priority (higher class), its class is raised.
// It returns the job id and whether a new job was created.
func (s *Tx) EnqueueJob(ctx context.Context, j NewJob) (int64, bool, error) {
	now := s.unix()
	var src any
	if j.SourceID > 0 {
		src = j.SourceID
	}
	res, err := s.meta.ExecContext(ctx, `INSERT INTO jobs (kind, source_id, target, priority_class, state, not_before,
		max_attempts, dedupe_key, created_at, updated_at) VALUES (?,?,?,?, 'queued', 0,?,?,?,?)
		ON CONFLICT (dedupe_key) WHERE state IN ('queued','running') DO NOTHING`,
		j.Kind, src, j.Target, j.Priority, DefaultMaxAttempts, j.DedupeKey, now, now)
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		id, err := res.LastInsertId()
		return id, true, err
	}
	var id int64
	var prio int
	var state string
	var parked bool // owned by a paused/pending/cancelled source: PickJob never runs it
	if err := s.meta.QueryRowContext(ctx, `SELECT j.id, j.priority_class, j.state,
		  j.source_id IS NOT NULL AND COALESCE(src.state, '') NOT IN ('active','done_backfill')
		FROM jobs j LEFT JOIN sources src ON src.id = j.source_id
		WHERE j.dedupe_key = ? AND j.state IN ('queued','running')`,
		j.DedupeKey).Scan(&id, &prio, &state, &parked); err != nil {
		return 0, false, err
	}
	if parked && state == model.JobQueued {
		// Dedupe keys are global (one fetch per work), so a parked job would swallow every
		// other source's or the user's request for the same target: hand it to this caller.
		if _, err := s.meta.ExecContext(ctx, `UPDATE jobs SET source_id = ?, priority_class = MIN(priority_class, ?), updated_at = ?
			WHERE id = ? AND state = 'queued'`, src, j.Priority, now, id); err != nil {
			return id, false, err
		}
		s.log.Debug("parked job taken over", "job_id", id, "source_id", j.SourceID)
		return id, false, nil
	}
	if j.Priority < prio {
		if _, err := s.meta.ExecContext(ctx, `UPDATE jobs SET priority_class = ?, updated_at = ? WHERE id = ? AND state = 'queued'`,
			j.Priority, now, id); err != nil {
			return id, false, err
		}
	}
	return id, false, nil
}

// PickJob claims the next runnable job of the given kinds and marks it running.
// Order: priority class, then source priority (desc), then least recently served source
// (round-robin fairness), then job id. Jobs of paused/pending/cancelled sources are skipped.
func (s *Tx) PickJob(ctx context.Context, kinds []string, now time.Time) (model.Job, error) {
	if len(kinds) == 0 {
		return model.Job{}, ErrNoJob
	}
	tx := s.meta
	args := []any{now.Unix()}
	for _, k := range kinds {
		args = append(args, k)
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColsJ+` FROM jobs j
		LEFT JOIN sources src ON src.id = j.source_id
		WHERE j.state = 'queued' AND j.not_before <= ? AND j.kind IN (`+placeholders(len(kinds))+`)
		  AND (j.source_id IS NULL OR src.state IN ('active','done_backfill'))
		ORDER BY j.priority_class, COALESCE(src.priority, 0) DESC, COALESCE(src.last_served_ms, 0), j.id
		LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Job{}, ErrNoJob
	}
	if err != nil {
		return model.Job{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'running', attempts = attempts + 1, started_at = ?, updated_at = ?
		WHERE id = ?`, now.Unix(), now.Unix(), j.ID); err != nil {
		return model.Job{}, err
	}
	if j.SourceID > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_served_ms = ? WHERE id = ?`, now.UnixMilli(), j.SourceID); err != nil {
			return model.Job{}, err
		}
	}
	j.State, j.Attempts, j.StartedAt = model.JobRunning, j.Attempts+1, now
	return j, nil
}

// FinishJob marks a running job done or failed.
func (s *Tx) FinishJob(ctx context.Context, id int64, state, lastError string) error {
	now := s.unix()
	_, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = ?, last_error = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		state, lastError, now, now, id)
	return err
}

// RetryJob re-queues a running job after notBefore, or fails it when attempts are exhausted.
// It returns the resulting state.
func (s *Tx) RetryJob(ctx context.Context, id int64, notBefore time.Time, lastError string) (string, error) {
	now := s.unix()
	var attempts, max int
	if err := s.meta.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM jobs WHERE id = ?`, id).Scan(&attempts, &max); err != nil {
		return "", err
	}
	if attempts >= max {
		return model.JobFailed, s.FinishJob(ctx, id, model.JobFailed, lastError)
	}
	_, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = 'queued', not_before = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		notBefore.Unix(), lastError, now, id)
	return model.JobQueued, err
}

// RequeueJob puts a running job back without counting the attempt (e.g. shutdown, breaker).
func (s *Tx) RequeueJob(ctx context.Context, id int64, notBefore time.Time) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = 'queued', attempts = MAX(attempts - 1, 0), not_before = ?, updated_at = ?
		WHERE id = ? AND state = 'running'`, notBefore.Unix(), s.unix(), id)
	return err
}

// CodeCrashed is the last_error of a job that was running when the daemon died.
const CodeCrashed = "crashed"

// RecoverRunning re-queues jobs left running by a crashed or killed daemon. Unlike RequeueJob
// (clean shutdown) the attempt counts: a job that kills the process (OOM, SIGKILL on a huge
// page) must not crash-loop the daemon, re-fetching the same page on every restart. A job with
// no attempts left is failed instead.
func (s *Tx) RecoverRunning(ctx context.Context) (int64, error) {
	now := s.unix()
	failed, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = 'failed', last_error = ?, finished_at = ?, updated_at = ?
		WHERE state = 'running' AND attempts >= max_attempts`, CodeCrashed, now, now)
	if err != nil {
		return 0, err
	}
	requeued, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = 'queued', last_error = ?, updated_at = ?
		WHERE state = 'running'`, CodeCrashed, now)
	if err != nil {
		return 0, err
	}
	nf, _ := failed.RowsAffected()
	nq, _ := requeued.RowsAffected()
	return nf + nq, nil
}

func (s *ReadTx) GetJob(ctx context.Context, id int64) (model.Job, error) {
	j, err := scanJob(s.meta.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNoJob
	}
	return j, err
}

// FindJobs returns up to f.Limit jobs matching f, skipping the first f.Offset.
func (s *ReadTx) FindJobs(ctx context.Context, f model.JobFilter) ([]model.Job, error) {
	if f.Offset < 0 || f.Limit < 1 {
		return nil, fmt.Errorf("store: find jobs: bad page (offset %d, limit %d)", f.Offset, f.Limit)
	}
	where, args := jobWhere(f)
	q := `SELECT ` + jobCols + ` FROM jobs` + where
	switch f.Order {
	case model.JobsOldest:
		q += ` ORDER BY id ASC`
	case model.JobsLastStarted:
		q += ` ORDER BY started_at DESC, id DESC`
	default:
		q += ` ORDER BY id DESC`
	}
	q += ` LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)
	rows, err := s.meta.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountJobs returns the number of jobs matching f's filters.
func (s *ReadTx) CountJobs(ctx context.Context, f model.JobFilter) (int, error) {
	where, args := jobWhere(f)
	var n int
	err := s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`+where, args...).Scan(&n)
	return n, err
}

// jobWhere builds the WHERE clause (with a leading space) and its args for f's filters.
func jobWhere(f model.JobFilter) (string, []any) {
	q := ` WHERE 1=1`
	var args []any
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, f.State)
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.SourceID > 0 {
		q += ` AND source_id = ?`
		args = append(args, f.SourceID)
	}
	if f.DedupeKey != "" {
		q += ` AND dedupe_key = ?`
		args = append(args, f.DedupeKey)
	}
	if f.DedupePrefix != "" {
		q += ` AND dedupe_key LIKE ? ESCAPE '\'`
		args = append(args, likeEscape(f.DedupePrefix)+"%")
	}
	if f.NotQueued {
		q += ` AND state != 'queued'`
	}
	return q, args
}

// JobEvent appends a content-free trace event. Its detail is parts merged into one JSON object
// (see JobDetailJSON).
func (s *Tx) JobEvent(ctx context.Context, jobID int64, event string, parts ...any) error {
	b, err := JobDetailJSON(parts...)
	if err != nil {
		return err
	}
	_, err = s.meta.ExecContext(ctx, `INSERT INTO job_events (job_id, ts, event, detail) VALUES (?,?,?,?)`, jobID, s.unix(), event, string(b))
	return err
}

// JobDetailJSON merges parts into one JSON object with sorted keys. Each part must marshal to
// an object or null (a nil part, or a nil pointer, adds nothing); a key in two parts is an
// error, as one would silently hide the other.
func JobDetailJSON(parts ...any) ([]byte, error) {
	out := map[string]json.RawMessage{}
	for i, p := range parts {
		b, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("job event detail part %d: %w", i, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("job event detail part %d (%T) is not a JSON object", i, p)
		}
		for k, v := range m {
			if _, dup := out[k]; dup {
				return nil, fmt.Errorf("job event detail key %q is in two parts", k)
			}
			out[k] = v
		}
	}
	return json.Marshal(out)
}

type JobEventRow struct {
	Time   time.Time
	Event  string
	Detail string
}

func (s *ReadTx) JobEvents(ctx context.Context, jobID int64) ([]JobEventRow, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT ts, event, detail FROM job_events WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobEventRow
	for rows.Next() {
		var r JobEventRow
		var ts int64
		if err := rows.Scan(&ts, &r.Event, &r.Detail); err != nil {
			return nil, err
		}
		r.Time = time.Unix(ts, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneJobs deletes finished jobs (and their events) older than the given ages.
func (s *Tx) PruneJobs(ctx context.Context, doneAge, failedAge time.Duration) (int64, error) {
	now := s.now()
	res, err := s.meta.ExecContext(ctx, `DELETE FROM jobs WHERE (state IN ('done','cancelled') AND finished_at < ?)
		OR (state = 'failed' AND finished_at < ?)`, now.Add(-doneAge).Unix(), now.Add(-failedAge).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// JobCounts groups jobs by kind and state.
func (s *ReadTx) JobCounts(ctx context.Context) (map[string]map[string]int, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT kind, state, COUNT(*) FROM jobs GROUP BY kind, state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var k, st string
		var n int
		if err := rows.Scan(&k, &st, &n); err != nil {
			return nil, err
		}
		if out[k] == nil {
			out[k] = map[string]int{}
		}
		out[k][st] = n
	}
	return out, rows.Err()
}

// FailedWorkJobs returns the distinct work ids of a source's failed work jobs that are still
// unresolved: no work job for that id is active, and none succeeded after the failure (work
// fetches are global, so a later success from any source counts).
func (s *ReadTx) FailedWorkJobs(ctx context.Context, sourceID int64) ([]string, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT DISTINCT f.target FROM jobs f WHERE f.source_id = ? AND f.kind = 'work' AND f.state = 'failed'
		AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.kind = 'work' AND j.target = f.target
			AND (j.state IN ('queued','running') OR (j.state = 'done' AND j.id > f.id)))`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CancelSourceJobs cancels a source's queued jobs; running jobs finish on their own.
func (s *Tx) CancelSourceJobs(ctx context.Context, sourceID int64) (int64, error) {
	res, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = 'cancelled', updated_at = ?, finished_at = ?
		WHERE source_id = ? AND state = 'queued'`, s.unix(), s.unix(), sourceID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.log.Info("source jobs cancelled", "source_id", sourceID, "jobs", n)
	}
	return n, nil
}

// ClaimJob marks one specific queued job running (for `fetch-work --now`).
func (s *Tx) ClaimJob(ctx context.Context, id int64, now time.Time) (model.Job, error) {
	res, err := s.meta.ExecContext(ctx, `UPDATE jobs SET state = 'running', attempts = attempts + 1, started_at = ?, updated_at = ?
		WHERE id = ? AND state = 'queued'`, now.Unix(), now.Unix(), id)
	if err != nil {
		return model.Job{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Job{}, ErrNoJob
	}
	return s.GetJob(ctx, id)
}

// CountActiveJobs counts queued/running jobs of a kind.
func (s *ReadTx) CountActiveJobs(ctx context.Context, kind string) (int, error) {
	var n int
	err := s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind = ? AND state IN ('queued','running')`, kind).Scan(&n)
	return n, err
}
