package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SourceState values match the sources.state CHECK constraint.
type SourceState string

const (
	SourcePendingConfirm SourceState = "pending_confirm"
	SourceActive         SourceState = "active"
	SourcePaused         SourceState = "paused"
	SourceCancelled      SourceState = "cancelled"
	SourceDoneBackfill   SourceState = "done_backfill"
)

var ErrNoSource = errors.New("store: no such source")

// ErrStateConflict means a source was not in one of the required states (e.g. it was paused or
// cancelled concurrently). Callers racing with the CLI can treat it as "try again later".
var ErrStateConflict = errors.New("store: source state conflict")

// SourceRow is one sources row. Target and FiltersJSON may hold tag/user names: never log them.
type SourceRow struct {
	ID             int64
	Kind           string
	Target         string
	FiltersJSON    string
	Path           string
	State          SourceState
	BackfillOrder  string
	PollInterval   time.Duration
	Priority       int
	TotalWorks     int // -1 unknown
	EstRequests    int // -1 unknown
	BackfillCursor string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastPollAt     time.Time
	NextPollAt     time.Time
	LastFullScanAt time.Time
	BackfillDoneAt time.Time
	LastError      string
	MemberWorks    int // works linked via source_works (filled by Get/List)
	FetchedWorks   int // of those, fetched at least once
}

const sourceCols = `id, kind, target, filters_json, path, state, backfill_order, poll_interval_s, priority,
	total_works, est_requests, backfill_cursor, created_at, updated_at, last_poll_at, next_poll_at,
	last_full_scan_at, backfill_done_at, last_error,
	(SELECT COUNT(*) FROM source_works sw WHERE sw.source_id = sources.id),
	(SELECT COUNT(*) FROM source_works sw JOIN works w ON w.id = sw.work_id
	  WHERE sw.source_id = sources.id AND w.last_fetched > 0)`

func scanSource(row interface{ Scan(...any) error }) (SourceRow, error) {
	var r SourceRow
	var poll, created, updated, lastPoll, nextPoll, lastScan, done int64
	err := row.Scan(&r.ID, &r.Kind, &r.Target, &r.FiltersJSON, &r.Path, &r.State, &r.BackfillOrder, &poll,
		&r.Priority, &r.TotalWorks, &r.EstRequests, &r.BackfillCursor, &created, &updated, &lastPoll, &nextPoll,
		&lastScan, &done, &r.LastError, &r.MemberWorks, &r.FetchedWorks)
	if err != nil {
		return r, err
	}
	r.PollInterval = time.Duration(poll) * time.Second
	r.CreatedAt, r.UpdatedAt = unixTime(created), unixTime(updated)
	r.LastPollAt, r.NextPollAt = unixTime(lastPoll), unixTime(nextPoll)
	r.LastFullScanAt, r.BackfillDoneAt = unixTime(lastScan), unixTime(done)
	return r, nil
}

// InsertSource creates a source. It fails with ErrSourceExists if (kind,target,filters) exists.
var ErrSourceExists = errors.New("store: source already exists")

func (s *Store) InsertSource(ctx context.Context, r SourceRow) (int64, error) {
	now := s.unix()
	res, err := s.meta.ExecContext(ctx, `INSERT INTO sources (kind, target, filters_json, path, state,
		backfill_order, poll_interval_s, priority, total_works, est_requests, backfill_cursor, created_at,
		updated_at, next_poll_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (kind, target, filters_json) DO NOTHING`,
		r.Kind, r.Target, r.FiltersJSON, r.Path, string(r.State), r.BackfillOrder, int64(r.PollInterval/time.Second),
		r.Priority, r.TotalWorks, r.EstRequests, r.BackfillCursor, now, now, zeroUnix(r.NextPollAt))
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrSourceExists
	}
	id, err := res.LastInsertId()
	s.log.Info("source inserted", "source_id", id, "kind", r.Kind, "state", string(r.State), "total_works", r.TotalWorks)
	return id, err
}

// FindSource returns the id of an existing (kind,target,filters) source, or 0.
func (s *Store) FindSource(ctx context.Context, kind, target, filtersJSON string) (int64, error) {
	var id int64
	err := s.meta.QueryRowContext(ctx, `SELECT id FROM sources WHERE kind = ? AND target = ? AND filters_json = ?`,
		kind, target, filtersJSON).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) GetSource(ctx context.Context, id int64) (SourceRow, error) {
	r, err := scanSource(s.meta.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM sources WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoSource
	}
	return r, err
}

func (s *Store) ListSources(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT `+sourceCols+` FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceRow
	for rows.Next() {
		r, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetSourceState moves a source to `to` only if its current state is one of `from`.
func (s *Store) SetSourceState(ctx context.Context, id int64, to SourceState, from ...SourceState) error {
	q := `UPDATE sources SET state = ?, updated_at = ? WHERE id = ?`
	args := []any{string(to), s.unix(), id}
	if len(from) > 0 {
		q += ` AND state IN (` + placeholders(len(from)) + `)` //nolint:gosec // G202: only "?,?,..." is concatenated; values are bound args
		for _, f := range from {
			args = append(args, string(f))
		}
	}
	res, err := s.meta.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		cur, err := s.GetSource(ctx, id)
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: source %d is %s, cannot become %s", ErrStateConflict, id, cur.State, to)
	}
	s.log.Info("source state", "source_id", id, "state", string(to))
	return nil
}

// UpdateSourceProgress records totals/cursor/error after listing work. Zero values are written.
func (s *Store) UpdateSourceProgress(ctx context.Context, r SourceRow) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE sources SET total_works = ?, est_requests = ?, backfill_cursor = ?,
		last_poll_at = ?, next_poll_at = ?, last_full_scan_at = ?, backfill_done_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?`, r.TotalWorks, r.EstRequests, r.BackfillCursor, zeroUnix(r.LastPollAt), zeroUnix(r.NextPollAt),
		zeroUnix(r.LastFullScanAt), zeroUnix(r.BackfillDoneAt), r.LastError, s.unix(), r.ID)
	return err
}

// UpdateSourceSettings changes a source's poll interval and priority (user settings).
func (s *Store) UpdateSourceSettings(ctx context.Context, id int64, poll time.Duration, priority int) error {
	res, err := s.meta.ExecContext(ctx, `UPDATE sources SET poll_interval_s = ?, priority = ?, updated_at = ? WHERE id = ?`,
		int64(poll/time.Second), priority, s.unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSource
	}
	s.log.Info("source settings", "source_id", id, "poll_interval", poll.String(), "priority", priority)
	return nil
}

// DeleteSource removes a source and its memberships (works are kept) after cancelling its jobs.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	if _, err := s.CancelSourceJobs(ctx, id); err != nil {
		return err
	}
	res, err := s.meta.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSource
	}
	s.log.Info("source deleted", "source_id", id)
	return nil
}

// LinkSourceWorks records that works appeared in a source's listing.
func (s *Store) LinkSourceWorks(ctx context.Context, sourceID int64, workIDs []int64) error {
	if len(workIDs) == 0 {
		return nil
	}
	tx, err := s.meta.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	now := s.unix()
	for _, w := range workIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_works (source_id, work_id, first_seen, last_seen) VALUES (?,?,?,?)
			ON CONFLICT (source_id, work_id) DO UPDATE SET last_seen = excluded.last_seen`, sourceID, w, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Cursor is the backfill position stored in sources.backfill_cursor (JSON).
type Cursor struct {
	NextPage int    `json:"next_page"` // 0 = listing backfill done
	Order    string `json:"order"`
	// Date windows on revised_at, used when a source exceeds AO3's 100k result window.
	// Backfill walks windows backwards in time: [WindowFrom, WindowTo].
	WindowFrom string `json:"window_from,omitempty"` // YYYY-MM-DD
	WindowTo   string `json:"window_to,omitempty"`
	SpanDays   int    `json:"span_days,omitempty"`
	// Rescan marks a periodic full re-walk (stats refresh + deletion detection) started at
	// RescanStarted (unix seconds). Works not seen since then get a verification fetch.
	Rescan        bool  `json:"rescan,omitempty"`
	RescanStarted int64 `json:"rescan_started,omitempty"`
	// BlockedParser is set to parse.Version when the NextPage listing failed to parse. Backfill
	// pauses until the binary ships a different parser version.
	BlockedParser int `json:"blocked_parser,omitempty"`
}

func (c Cursor) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

func ParseCursor(s string) (Cursor, error) {
	var c Cursor
	if s == "" || s == "{}" {
		return c, nil
	}
	err := json.Unmarshal([]byte(s), &c)
	return c, err
}

// SourceWorksNeedingFetch returns a source's member works that are ok and were never fetched or
// changed since their last fetch (same rule as WorkNeedsFetch), oldest first.
func (s *Store) SourceWorksNeedingFetch(ctx context.Context, sourceID int64, limit int) ([]int64, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT w.id FROM source_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.source_id = ? AND w.status = 'ok' AND (w.last_fetched = 0 OR w.fetched_revised != w.revised_at
			OR w.fetched_chapters != w.chapters_posted OR w.fetched_words != w.words)
		ORDER BY w.id LIMIT ?`, sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SourceWorksNotSeenSince returns ok works linked to a source whose last listing sighting in
// that source is before since (candidates for deletion checks).
func (s *Store) SourceWorksNotSeenSince(ctx context.Context, sourceID int64, since time.Time) ([]int64, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT sw.work_id FROM source_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.source_id = ? AND sw.last_seen < ? AND w.status = 'ok' ORDER BY sw.work_id`, sourceID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
