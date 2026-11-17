package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// UpsertSeries stores a series page's metadata (description, notes, dates, stats, creators)
// and marks the series fetched.
func (s *Store) UpsertSeries(ctx context.Context, ser model.Series) error {
	tx, err := s.meta.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	now := s.unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO series (id, title, summary_html, notes_html, complete, words, works_count,
		bookmarks, begun, updated, first_seen, last_fetched) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET title = excluded.title, summary_html = excluded.summary_html,
		notes_html = excluded.notes_html, complete = excluded.complete, words = excluded.words,
		works_count = excluded.works_count, bookmarks = excluded.bookmarks, begun = excluded.begun,
		updated = excluded.updated, last_fetched = excluded.last_fetched`,
		ser.ID, ser.Title, ser.SummaryHTML, ser.NotesHTML, boolInt(ser.Complete), ser.Words, ser.WorksCount,
		ser.Bookmarks, ser.Begun, ser.Updated, now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM series_creators WHERE series_id = ?`, ser.ID); err != nil {
		return err
	}
	for _, c := range ser.Creators {
		id, err := creatorID(ctx, tx, c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO series_creators (series_id, creator_id) VALUES (?,?)`, ser.ID, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.log.Debug("series stored", "series_id", ser.ID, "works_count", ser.WorksCount, "words", ser.Words,
		"complete", ser.Complete, "creators", len(ser.Creators), "summary_len", len(ser.SummaryHTML))
	return nil
}

// ErrNoSeries means no series row has the requested id.
var ErrNoSeries = errors.New("store: no such series")

// SeriesRow is a stored series: its page metadata plus when it was last fetched. Creators are
// not loaded. It holds fanwork content: never log its text fields.
type SeriesRow struct {
	model.Series
	LastFetched time.Time // zero if the series page was never fetched
}

// Series returns a stored series, or ErrNoSeries.
func (s *Store) Series(ctx context.Context, id int64) (SeriesRow, error) {
	var r SeriesRow
	r.ID = id
	var complete int
	var fetched int64
	err := s.meta.QueryRowContext(ctx, `SELECT title, summary_html, notes_html, complete, words, works_count,
		bookmarks, begun, updated, last_fetched FROM series WHERE id = ?`, id).Scan(&r.Title, &r.SummaryHTML,
		&r.NotesHTML, &complete, &r.Words, &r.WorksCount, &r.Bookmarks, &r.Begun, &r.Updated, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoSeries
	}
	r.Complete, r.LastFetched = complete == 1, unixTime(fetched)
	return r, err
}

// MarkSeriesFetched records a fetch attempt that produced no metadata (404, restricted, hidden).
func (s *Store) MarkSeriesFetched(ctx context.Context, id int64) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE series SET last_fetched = ? WHERE id = ?`, s.unix(), id)
	return err
}

// SeriesNeedingInfo returns ids of series that are not themselves sources and were never
// fetched, fetched before `before`, or fetched before one of their mirrored works was revised.
func (s *Store) SeriesNeedingInfo(ctx context.Context, before time.Time, limit int) ([]int64, error) {
	return queryInt64s(ctx, s.meta, `SELECT se.id FROM series se
		WHERE NOT EXISTS (SELECT 1 FROM sources so WHERE so.kind = 'series' AND so.target = CAST(se.id AS TEXT)
			AND so.state IN ('pending_confirm','active','paused','done_backfill'))
		  AND (se.last_fetched = 0 OR se.last_fetched < ?
		       OR se.last_fetched < (SELECT COALESCE(MAX(CAST(strftime('%s', w.revised_at) AS INTEGER)), 0)
		                             FROM series_works sw JOIN works w ON w.id = sw.work_id
		                             WHERE sw.series_id = se.id AND w.revised_at != ''))
		ORDER BY se.last_fetched, se.id LIMIT ?`, before.Unix(), limit)
}

// SeriesMember is one work listed on a series page.
type SeriesMember struct {
	WorkID   int64
	Position int // AO3 part number from the blurb (0 if absent)
}

// SetSeriesMembers records the works seen on a series page in series_works, including works we
// do not mirror (no works row). Rows for fetched works are left alone: their work page is the
// authority for their position. Only page 1 is fetched for non-source series, so for series
// longer than one page this covers the first page only.
func (s *Store) SetSeriesMembers(ctx context.Context, seriesID int64, members []SeriesMember) error {
	tx, err := s.meta.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	// Drop stale unfetched members (a work may have left the series), keep fetched ones.
	if _, err := tx.ExecContext(ctx, `DELETE FROM series_works WHERE series_id = ?
		AND work_id NOT IN (SELECT id FROM works WHERE last_fetched > 0)`, seriesID); err != nil {
		return err
	}
	for _, m := range members {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO series_works (series_id, work_id, position) VALUES (?,?,?)`,
			seriesID, m.WorkID, m.Position); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PartialSeries is a series that is not a source and has fewer mirrored works than AO3 lists.
type PartialSeries struct {
	ID               int64
	WorksTotal       int     // AO3's count (series page stats)
	Mirrored         int     // works in the series we have fetched
	MissingPositions []int   // AO3 part numbers (1..WorksTotal) none of our fetched works has
	MissingWorkIDs   []int64 // series_works members we have not fetched (from the series page)
}

// PartialSeriesList returns partial series, most missing works first. Content-free (ids and
// counts only).
func (s *Store) PartialSeriesList(ctx context.Context, limit int) ([]PartialSeries, error) {
	out, err := s.partialSeriesRows(ctx, limit)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillMissing(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) partialSeriesRows(ctx context.Context, limit int) (out []PartialSeries, err error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT se.id, se.works_count,
			(SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id = sw.work_id
			 WHERE sw.series_id = se.id AND w.last_fetched > 0) AS mirrored
		FROM series se
		WHERE se.last_fetched > 0
		  AND NOT EXISTS (SELECT 1 FROM sources so WHERE so.kind = 'series' AND so.target = CAST(se.id AS TEXT)
			AND so.state IN ('pending_confirm','active','paused','done_backfill'))
		  AND se.works_count > (SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id = sw.work_id
			 WHERE sw.series_id = se.id AND w.last_fetched > 0)
		ORDER BY se.works_count - mirrored DESC, se.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var p PartialSeries
		if err := rows.Scan(&p.ID, &p.WorksTotal, &p.Mirrored); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) fillMissing(ctx context.Context, p *PartialSeries) error {
	positions, err := queryInt64s(ctx, s.meta, `SELECT sw.position FROM series_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.series_id = ? AND w.last_fetched > 0`, p.ID)
	if err != nil {
		return err
	}
	have := map[int64]bool{}
	for _, pos := range positions {
		have[pos] = true
	}
	for pos := 1; pos <= p.WorksTotal; pos++ {
		if !have[int64(pos)] {
			p.MissingPositions = append(p.MissingPositions, pos)
		}
	}
	p.MissingWorkIDs, err = queryInt64s(ctx, s.meta, `SELECT sw.work_id FROM series_works sw
		WHERE sw.series_id = ? AND NOT EXISTS (SELECT 1 FROM works w WHERE w.id = sw.work_id AND w.last_fetched > 0)
		ORDER BY sw.position, sw.work_id`, p.ID)
	return err
}
