package store

import (
	"context"
	"database/sql"
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

// sourceBaseCols are the sources columns; sourceCols adds the member counts, which scan every
// membership of the source (fine for status and show, too slow for every scheduler round).
const (
	sourceBaseCols = `id, kind, target, filters_json, path, state, backfill_order, poll_interval_s, priority,
	total_works, est_requests, backfill_cursor, created_at, updated_at, last_poll_at, next_poll_at,
	last_full_scan_at, backfill_done_at, last_error`
	sourceCountCols = `(SELECT COUNT(*) FROM source_works sw WHERE sw.source_id = sources.id),
	(SELECT COUNT(*) FROM source_works sw JOIN works w ON w.id = sw.work_id
	  WHERE sw.source_id = sources.id AND w.last_fetched > 0)`
	sourceCols = sourceBaseCols + `, ` + sourceCountCols
)

func scanSource(row interface{ Scan(...any) error }) (SourceRow, error) {
	return scanSourceRow(row, true)
}

func scanSourceRow(row interface{ Scan(...any) error }, counts bool) (SourceRow, error) {
	var r SourceRow
	var poll, created, updated, lastPoll, nextPoll, lastScan, done int64
	dest := []any{&r.ID, &r.Kind, &r.Target, &r.FiltersJSON, &r.Path, &r.State, &r.BackfillOrder, &poll,
		&r.Priority, &r.TotalWorks, &r.EstRequests, &r.BackfillCursor, &created, &updated, &lastPoll, &nextPoll,
		&lastScan, &done, &r.LastError}
	if counts {
		dest = append(dest, &r.MemberWorks, &r.FetchedWorks)
	}
	err := row.Scan(dest...)
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

func (s *Tx) InsertSource(ctx context.Context, r SourceRow) (int64, error) {
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
func (s *ReadTx) FindSource(ctx context.Context, kind, target, filtersJSON string) (int64, error) {
	var id int64
	err := s.meta.QueryRowContext(ctx, `SELECT id FROM sources WHERE kind = ? AND target = ? AND filters_json = ?`,
		kind, target, filtersJSON).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *ReadTx) GetSource(ctx context.Context, id int64) (SourceRow, error) {
	r, err := scanSource(s.meta.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM sources WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoSource
	}
	return r, err
}

func (s *ReadTx) ListSources(ctx context.Context) ([]SourceRow, error) {
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

// GetSourceRow is GetSource without the member counts (MemberWorks and FetchedWorks are 0),
// for crawl and settings paths that run on every page: the counts scan all of the source's
// memberships.
func (s *ReadTx) GetSourceRow(ctx context.Context, id int64) (SourceRow, error) {
	r, err := scanSourceRow(s.meta.QueryRowContext(ctx, `SELECT `+sourceBaseCols+` FROM sources WHERE id = ?`, id), false)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoSource
	}
	return r, err
}

// SourceExists reports whether a source row exists (any state), without the member counts.
func (s *ReadTx) SourceExists(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM sources WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}

// SchedulableSources returns the active and done_backfill sources without member counts
// (MemberWorks and FetchedWorks are 0): the scheduler reads them before every job pick.
func (s *ReadTx) SchedulableSources(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT `+sourceBaseCols+` FROM sources
		WHERE state IN ('active','done_backfill') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceRow
	for rows.Next() {
		r, err := scanSourceRow(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceWorkCounts returns how many works a source has seen and how many of those were fetched.
func (s *ReadTx) SourceWorkCounts(ctx context.Context, id int64) (seen, fetched int, err error) {
	err = s.meta.QueryRowContext(ctx, `SELECT `+sourceCountCols+` FROM sources WHERE id = ?`, id).Scan(&seen, &fetched)
	return seen, fetched, err
}

// SetSourceState moves a source to `to` only if its current state is one of `from`.
func (s *Tx) SetSourceState(ctx context.Context, id int64, to SourceState, from ...SourceState) error {
	q := `UPDATE sources SET state = ?, updated_at = ? WHERE id = ?`
	args := []any{string(to), s.unix(), id}
	if len(from) > 0 {
		q += ` AND state IN (` + placeholders(len(from)) + `)` // only "?,?,..." is concatenated; values are bound args
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
func (s *Tx) UpdateSourceProgress(ctx context.Context, r SourceRow) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE sources SET total_works = ?, est_requests = ?, backfill_cursor = ?,
		last_poll_at = ?, next_poll_at = ?, last_full_scan_at = ?, backfill_done_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?`, r.TotalWorks, r.EstRequests, r.BackfillCursor, zeroUnix(r.LastPollAt), zeroUnix(r.NextPollAt),
		zeroUnix(r.LastFullScanAt), zeroUnix(r.BackfillDoneAt), r.LastError, s.unix(), r.ID)
	return err
}

// UpdateSourceSettings changes a source's poll interval and priority (user settings).
func (s *Tx) UpdateSourceSettings(ctx context.Context, id int64, poll time.Duration, priority int) error {
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
func (s *Tx) DeleteSource(ctx context.Context, id int64) error {
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

// LinkSourceWorks records that works appeared in a source's listing: new memberships are
// added and every one's last_seen is set to now. It returns how many were new to the source.
// Only listing sightings call it; a work fetch uses AddSourceWork, which never refreshes
// last_seen (so rescans can tell a work that left the source).
func (s *Tx) LinkSourceWorks(ctx context.Context, sourceID int64, workIDs []int64) (int, error) {
	now := s.unix()
	added := 0
	for _, w := range workIDs {
		res, err := s.meta.ExecContext(ctx, `INSERT INTO source_works (source_id, work_id, first_seen, last_seen) VALUES (?,?,?,?)
			ON CONFLICT (source_id, work_id) DO NOTHING`, sourceID, w, now, now)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			added++
			continue
		}
		if _, err := s.meta.ExecContext(ctx, `UPDATE source_works SET last_seen = ?, missed_rescans = 0
			WHERE source_id = ? AND work_id = ?`, now, sourceID, w); err != nil {
			return added, err
		}
	}
	return added, nil
}

// AddSourceWork makes a work a member of a source if it is not one yet (a fetch for that
// source, e.g. a single-work source). An existing membership is left alone: last_seen and
// missed_rescans only change on listing sightings and rescans, so a fetch never keeps a work
// in a source whose listing stopped showing it.
func (s *Tx) AddSourceWork(ctx context.Context, sourceID, workID int64) error {
	now := s.unix()
	_, err := s.meta.ExecContext(ctx, `INSERT INTO source_works (source_id, work_id, first_seen, last_seen) VALUES (?,?,?,?)
		ON CONFLICT (source_id, work_id) DO NOTHING`, sourceID, workID, now, now)
	return err
}

// MissedRescansToDrop is how many finished rescans in a row must miss a member work before a
// successful fetch of it drops the membership. One miss alone can be a work that shifted into
// an already-walked page during the hours-long walk.
const MissedRescansToDrop = 2

// RecordMissedRescan counts one more finished rescan of a source for every ok member work whose
// last listing sighting in it is before since, and returns those works in check. The caller
// fetches each one: a deleted/hidden answer marks the work (it stays a member and is no longer
// counted), and a successful fetch drops the membership once the count reaches
// MissedRescansToDrop (DropMissedSourceWorks). A listing sighting or a revival resets the count.
//
// A work whose last fetch gave up (fetch_failed_at set: adult gate, too large, parse failure...)
// cannot be checked, and checking it again would only spend a request on the same failure, so
// at MissedRescansToDrop it is dropped without one (counted in dropped, not returned in check).
func (s *Tx) RecordMissedRescan(ctx context.Context, sourceID int64, since time.Time) (check []int64, dropped int64, err error) {
	const okMember = `source_id = ? AND last_seen < ?
		AND EXISTS (SELECT 1 FROM works w WHERE w.id = source_works.work_id AND w.status = 'ok'`
	if _, err := s.meta.ExecContext(ctx, `UPDATE source_works SET missed_rescans = missed_rescans + 1
		WHERE `+okMember+`)`, sourceID, since.Unix()); err != nil {
		return nil, 0, err
	}
	res, err := s.meta.ExecContext(ctx, `DELETE FROM source_works WHERE missed_rescans >= ? AND `+okMember+`
		AND w.fetch_failed_at != 0)`, MissedRescansToDrop, sourceID, since.Unix())
	if err != nil {
		return nil, 0, err
	}
	if dropped, err = res.RowsAffected(); err != nil {
		return nil, 0, err
	}
	check, err = queryInt64s(ctx, s.meta, `SELECT work_id FROM source_works WHERE `+okMember+`)
		ORDER BY work_id`, sourceID, since.Unix())
	return check, dropped, err
}

// OkSourceMembers returns how many of a source's member works have status ok (the ones its
// listing is expected to show).
func (s *ReadTx) OkSourceMembers(ctx context.Context, sourceID int64) (int, error) {
	var n int
	err := s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.source_id = ? AND w.status = 'ok'`, sourceID).Scan(&n)
	return n, err
}

// DropMissedSourceWorks removes a work from every source whose last MissedRescansToDrop
// rescans in a row missed it. Call it only after a fetch found the work on AO3: it left those
// sources rather than being deleted. The work itself is kept. It returns how many were dropped.
func (s *Tx) DropMissedSourceWorks(ctx context.Context, workID int64) (int64, error) {
	res, err := s.meta.ExecContext(ctx, `DELETE FROM source_works WHERE work_id = ? AND missed_rescans >= ?`,
		workID, MissedRescansToDrop)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SourceWorksNeedingFetch returns a source's member works that are ok and were never fetched or
// changed since their last fetch (by revised date, chapters or words).
//
// Works whose last fetch failed after failedBefore are skipped (they would fail the same way
// every poll), and the rest come least recently checked first, so a backlog rotates instead of
// the same lowest ids filling the limit forever.
func (s *ReadTx) SourceWorksNeedingFetch(ctx context.Context, sourceID int64, failedBefore time.Time, limit int) ([]int64, error) {
	return queryInt64s(ctx, s.meta, `SELECT w.id FROM source_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.source_id = ? AND w.status = 'ok' AND w.fetch_failed_at < ?
		  AND (w.last_fetched = 0 OR w.fetched_revised != w.revised_at
			OR w.fetched_chapters != w.chapters_posted OR w.fetched_words != w.words)
		ORDER BY w.last_checked, w.id LIMIT ?`, sourceID, failedBefore.Unix(), limit)
}
