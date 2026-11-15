package store

import (
	"context"
	"database/sql"
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

func (s *Store) InsertRequest(ctx context.Context, r RequestRow) error {
	var job any
	if r.JobID > 0 {
		job = r.JobID
	}
	_, err := s.Meta.ExecContext(ctx, `INSERT INTO requests (ts, req_id, job_id, kind, path, status, class, reason,
		bytes, duration_ms, waited_ms, hop) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Time.Unix(), r.ReqID, job, r.Kind, r.Path, r.Status, r.Class, r.Reason, r.Bytes,
		r.Duration.Milliseconds(), r.Waited.Milliseconds(), r.Hop)
	return err
}

// PruneRequests deletes request rows older than age.
func (s *Store) PruneRequests(ctx context.Context, age time.Duration) (int64, error) {
	res, err := s.Meta.ExecContext(ctx, `DELETE FROM requests WHERE ts < ?`, s.now().Add(-age).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Counts is a content-free snapshot for `ao3mirror status`.
type Counts struct {
	WorksByStatus   map[string]int
	WorksFetched    int
	WorksNeverFetch int
	Tags            int
	Creators        int
	Series          int
	SourcesByState  map[string]int
	JobsByState     map[string]int
	RawPages        int
	RawBytes        int64
	RawZstdBytes    int64
	Requests1h      int
	Requests24h     int
	RequestsByClass map[string]int // last 24h
	MetaSchema      int
	ContentSchema   int
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	c := Counts{WorksByStatus: map[string]int{}, SourcesByState: map[string]int{}, JobsByState: map[string]int{}, RequestsByClass: map[string]int{}}
	now := s.now()
	grouped := []struct {
		db  *sql.DB
		q   string
		dst map[string]int
		arg []any
	}{
		{s.Meta, `SELECT status, COUNT(*) FROM works GROUP BY status`, c.WorksByStatus, nil},
		{s.Meta, `SELECT state, COUNT(*) FROM sources GROUP BY state`, c.SourcesByState, nil},
		{s.Meta, `SELECT state, COUNT(*) FROM jobs GROUP BY state`, c.JobsByState, nil},
		{s.Meta, `SELECT class, COUNT(*) FROM requests WHERE ts >= ? GROUP BY class`, c.RequestsByClass, []any{now.Add(-24 * time.Hour).Unix()}},
	}
	for _, g := range grouped {
		rows, err := g.db.QueryContext(ctx, g.q, g.arg...)
		if err != nil {
			return c, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return c, err
			}
			g.dst[k] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return c, err
		}
	}
	singles := []struct {
		db  *sql.DB
		q   string
		dst any
		arg []any
	}{
		{s.Meta, `SELECT COUNT(*) FROM works WHERE last_fetched > 0`, &c.WorksFetched, nil},
		{s.Meta, `SELECT COUNT(*) FROM works WHERE last_fetched = 0 AND status = 'ok'`, &c.WorksNeverFetch, nil},
		{s.Meta, `SELECT COUNT(*) FROM tags`, &c.Tags, nil},
		{s.Meta, `SELECT COUNT(*) FROM creators`, &c.Creators, nil},
		{s.Meta, `SELECT COUNT(*) FROM series`, &c.Series, nil},
		{s.Meta, `SELECT COUNT(*) FROM requests WHERE ts >= ?`, &c.Requests1h, []any{now.Add(-time.Hour).Unix()}},
		{s.Meta, `SELECT COUNT(*) FROM requests WHERE ts >= ?`, &c.Requests24h, []any{now.Add(-24 * time.Hour).Unix()}},
		{s.Content, `SELECT COUNT(*) FROM raw_pages`, &c.RawPages, nil},
		{s.Content, `SELECT COALESCE(SUM(size),0) FROM raw_pages`, &c.RawBytes, nil},
		{s.Content, `SELECT COALESCE(SUM(LENGTH(html_zstd)),0) FROM raw_pages`, &c.RawZstdBytes, nil},
		{s.Meta, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`, &c.MetaSchema, nil},
		{s.Content, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`, &c.ContentSchema, nil},
	}
	for _, q := range singles {
		if err := q.db.QueryRowContext(ctx, q.q, q.arg...).Scan(q.dst); err != nil {
			return c, err
		}
	}
	return c, nil
}
