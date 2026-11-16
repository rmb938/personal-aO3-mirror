package store

import (
	"context"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// UpsertSeries stores a series page's metadata (description, notes, dates, stats, creators)
// and marks the series fetched.
func (s *Store) UpsertSeries(ctx context.Context, ser model.Series) error {
	tx, err := s.Meta.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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

// MarkSeriesFetched records a fetch attempt that produced no metadata (404, restricted, hidden).
func (s *Store) MarkSeriesFetched(ctx context.Context, id int64) error {
	_, err := s.Meta.ExecContext(ctx, `UPDATE series SET last_fetched = ? WHERE id = ?`, s.unix(), id)
	return err
}

// SeriesNeedingInfo returns ids of series that are not themselves sources and were never
// fetched, fetched before `before`, or fetched before one of their mirrored works was revised.
func (s *Store) SeriesNeedingInfo(ctx context.Context, before time.Time, limit int) ([]int64, error) {
	rows, err := s.Meta.QueryContext(ctx, `SELECT se.id FROM series se
		WHERE NOT EXISTS (SELECT 1 FROM sources so WHERE so.kind = 'series' AND so.target = CAST(se.id AS TEXT)
			AND so.state IN ('pending_confirm','active','paused','done_backfill'))
		  AND (se.last_fetched = 0 OR se.last_fetched < ?
		       OR se.last_fetched < (SELECT COALESCE(MAX(CAST(strftime('%s', w.revised_at) AS INTEGER)), 0)
		                             FROM series_works sw JOIN works w ON w.id = sw.work_id
		                             WHERE sw.series_id = se.id AND w.revised_at != ''))
		ORDER BY se.last_fetched, se.id LIMIT ?`, before.Unix(), limit)
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

// WorkExists reports whether a work row exists.
func (s *Store) WorkExists(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.Meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM works WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}
