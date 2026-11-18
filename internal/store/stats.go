package store

import (
	"context"
	"errors"
	"time"
)

// RequestRow is one content-free HTTP log row (mirrors httpx.RequestRecord).
type RequestRow struct {
	Time     time.Time
	ReqID    string
	JobID    int64 // 0 = none
	Kind     string
	Path     string // already SafePath'd
	Status   int
	Class    string
	Reason   string
	Bytes    int
	Duration time.Duration
	Waited   time.Duration
	Hop      int
}

func (s *Tx) InsertRequest(ctx context.Context, r RequestRow) error {
	var job any
	if r.JobID > 0 {
		job = r.JobID
	}
	_, err := s.meta.ExecContext(ctx, `INSERT INTO requests (ts, req_id, job_id, kind, path, status, class, reason,
		bytes, duration_ms, waited_ms, hop) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Time.Unix(), r.ReqID, job, r.Kind, r.Path, r.Status, r.Class, r.Reason, r.Bytes,
		r.Duration.Milliseconds(), r.Waited.Milliseconds(), r.Hop)
	return err
}

// PruneRequests deletes request rows older than age.
func (s *Tx) PruneRequests(ctx context.Context, age time.Duration) (int64, error) {
	res, err := s.meta.ExecContext(ctx, `DELETE FROM requests WHERE ts < ?`, s.now().Add(-age).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecentRequests returns request rows newest first, optionally only non-ok, or only for a job
// or path (pathPrefix matches whole path segments: "/works/12" never matches /works/123).
func (s *ReadTx) RecentRequests(ctx context.Context, onlyErrors bool, jobID int64, pathPrefix string, limit int) ([]RequestRow, error) {
	q := `SELECT ts, req_id, COALESCE(job_id, 0), kind, path, status, class, reason, bytes, duration_ms, waited_ms, hop FROM requests WHERE 1=1`
	var args []any
	if onlyErrors {
		q += ` AND class != 'ok'`
	}
	if jobID > 0 {
		q += ` AND job_id = ?`
		args = append(args, jobID)
	}
	if pathPrefix != "" {
		// Whole path segments only: "/works/12" matches /works/12, /works/12?... and
		// /works/12/..., never /works/123.
		q += ` AND (path = ? OR path LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')`
		p := likeEscape(pathPrefix)
		args = append(args, pathPrefix, p+"?%", p+"/%")
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.meta.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestRow
	for rows.Next() {
		var r RequestRow
		var ts, dur, waited int64
		if err := rows.Scan(&ts, &r.ReqID, &r.JobID, &r.Kind, &r.Path, &r.Status, &r.Class, &r.Reason, &r.Bytes, &dur, &waited, &r.Hop); err != nil {
			return nil, err
		}
		r.Time, r.Duration, r.Waited = time.Unix(ts, 0), time.Duration(dur)*time.Millisecond, time.Duration(waited)*time.Millisecond
		out = append(out, r)
	}
	return out, rows.Err()
}

// Counts is a content-free snapshot for `ao3mirror status`.
type Counts struct {
	WorksByStatus   map[string]int
	WorksFetched    int
	WorksNeverFetch int
	ParseFailures   int // fetched but not parsed (parser_version 0); fixed by reparse after a parser update
	Tags            int
	Creators        int
	Series          int
	RawPages        int
	RawBytes        int64
	RawZstdBytes    int64
	Requests1h      int
	Requests24h     int
	RequestsByClass map[string]int // last 24h
}

// countGroups runs a `SELECT key, COUNT(*) ... GROUP BY key` query into dst. Its own function so
// the rows can be closed with defer (Counts runs several of these in a loop).
func countGroups(ctx context.Context, db querier, q string, dst map[string]int, args ...any) (err error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		dst[k] = n
	}
	return rows.Err()
}

func (s *ReadTx) Counts(ctx context.Context) (Counts, error) {
	c := Counts{WorksByStatus: map[string]int{}, RequestsByClass: map[string]int{}}
	now := s.now()
	grouped := []struct {
		db  querier
		q   string
		dst map[string]int
		arg []any
	}{
		{s.meta, `SELECT status, COUNT(*) FROM works GROUP BY status`, c.WorksByStatus, nil},
		{s.meta, `SELECT class, COUNT(*) FROM requests WHERE ts >= ? GROUP BY class`, c.RequestsByClass, []any{now.Add(-24 * time.Hour).Unix()}},
	}
	for _, g := range grouped {
		if err := countGroups(ctx, g.db, g.q, g.dst, g.arg...); err != nil {
			return c, err
		}
	}
	singles := []struct {
		db  querier
		q   string
		dst any
		arg []any
	}{
		{s.meta, `SELECT COUNT(*) FROM works WHERE last_fetched > 0`, &c.WorksFetched, nil},
		{s.meta, `SELECT COUNT(*) FROM works WHERE last_fetched > 0 AND parser_version = 0`, &c.ParseFailures, nil},
		{s.meta, `SELECT COUNT(*) FROM works WHERE last_fetched = 0 AND status = 'ok'`, &c.WorksNeverFetch, nil},
		{s.meta, `SELECT COUNT(*) FROM tags`, &c.Tags, nil},
		{s.meta, `SELECT COUNT(*) FROM creators`, &c.Creators, nil},
		{s.meta, `SELECT COUNT(*) FROM series`, &c.Series, nil},
		{s.meta, `SELECT COUNT(*) FROM requests WHERE ts >= ?`, &c.Requests1h, []any{now.Add(-time.Hour).Unix()}},
		{s.meta, `SELECT COUNT(*) FROM requests WHERE ts >= ?`, &c.Requests24h, []any{now.Add(-24 * time.Hour).Unix()}},
		{s.content, `SELECT COUNT(*) FROM raw_pages`, &c.RawPages, nil},
		{s.content, `SELECT COALESCE(SUM(size),0) FROM raw_pages`, &c.RawBytes, nil},
		{s.content, `SELECT COALESCE(SUM(LENGTH(html_zstd)),0) FROM raw_pages`, &c.RawZstdBytes, nil},
	}
	for _, q := range singles {
		if err := q.db.QueryRowContext(ctx, q.q, q.arg...).Scan(q.dst); err != nil {
			return c, err
		}
	}
	return c, nil
}
