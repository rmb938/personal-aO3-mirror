package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// UpsertSeries stores a series page's metadata (description, notes, dates, stats, creators)
// and marks the series fetched.
func (s *Tx) UpsertSeries(ctx context.Context, ser model.Series) error {
	tx := s.meta
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
	if _, err := tx.ExecContext(ctx, `UPDATE series SET fetched_revised = `+seriesMaxRevised("series")+` WHERE id = ?`, ser.ID); err != nil {
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
	// The title may have changed; updateFTS writes only the rows that did.
	if err := updateFTS(ctx, tx, seriesWorks(`?`), ser.ID); err != nil {
		return fmt.Errorf("update fts: %w", err)
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
func (s *ReadTx) Series(ctx context.Context, id int64) (SeriesRow, error) {
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

// seriesMaxRevised is the newest revised_at (YYYY-MM-DD, ” if none) of a series' mirrored
// works; seriesAlias names the series row.
func seriesMaxRevised(seriesAlias string) string {
	return `(SELECT COALESCE(MAX(w.revised_at), '') FROM series_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.series_id = ` + seriesAlias + `.id AND w.revised_at != '')`
}

// seriesIsSource is an EXISTS test: the series row seriesAlias is a live series source (one
// whose polls mirror it in full).
func seriesIsSource(seriesAlias string) string {
	return `EXISTS (SELECT 1 FROM sources so WHERE so.kind = 'series' AND so.target = CAST(` + seriesAlias + `.id AS TEXT)
		AND so.state IN ('pending_confirm','active','paused','done_backfill'))`
}

// MarkSeriesFetched records a fetch attempt of a series page: now, and the member revision it
// covered, so the series waits for series.info_ttl or a newer member revision. A successful
// fetch calls it after storing the page's blurbs; a failed one (404, restricted, unparseable)
// too, so it is not re-queued every round.
func (s *Tx) MarkSeriesFetched(ctx context.Context, id int64) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE series SET last_fetched = ?, fetched_revised = `+seriesMaxRevised("series")+`
		WHERE id = ?`, s.unix(), id)
	return err
}

// SeriesNeedingInfo returns ids of series that are not themselves sources and were never
// fetched, fetched before `before`, or fetched before one of their mirrored works was revised.
// Revisions compare as dates (revised_at vs fetched_revised), never against the fetch time:
// a blurb date can be a day ahead of UTC.
func (s *ReadTx) SeriesNeedingInfo(ctx context.Context, before time.Time, limit int) ([]int64, error) {
	return queryInt64s(ctx, s.meta, `SELECT se.id FROM series se
		WHERE NOT `+seriesIsSource("se")+`
		  AND (se.last_fetched = 0 OR se.last_fetched < ?
		       OR se.fetched_revised < `+seriesMaxRevised("se")+`)
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
func (s *Tx) SetSeriesMembers(ctx context.Context, seriesID int64, members []SeriesMember) error {
	tx := s.meta
	// Drop stale unfetched members (a work may have left the series), keep fetched ones. Their
	// ids are kept to reindex them: blurb-only works have works rows and index rows too.
	stale := `SELECT work_id FROM series_works WHERE series_id = ?
		AND work_id NOT IN (SELECT id FROM works WHERE last_fetched > 0)`
	dropped, err := queryInt64s(ctx, tx, stale, seriesID)
	if err != nil {
		return err
	}
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
	sel, args := seriesWorks(`?`), []any{seriesID}
	if len(dropped) > 0 {
		sel += ` UNION VALUES (?)` + strings.Repeat(`, (?)`, len(dropped)-1)
		for _, id := range dropped {
			args = append(args, id)
		}
	}
	if err := updateFTS(ctx, tx, sel, args...); err != nil {
		return fmt.Errorf("update fts: %w", err)
	}
	return nil
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
func (s *ReadTx) PartialSeriesList(ctx context.Context, limit int) ([]PartialSeries, error) {
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

func (s *ReadTx) partialSeriesRows(ctx context.Context, limit int) (out []PartialSeries, err error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT se.id, se.works_count,
			(SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id = sw.work_id
			 WHERE sw.series_id = se.id AND w.last_fetched > 0) AS mirrored
		FROM series se
		WHERE se.last_fetched > 0
		  AND NOT `+seriesIsSource("se")+`
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

func (s *ReadTx) fillMissing(ctx context.Context, p *PartialSeries) error {
	var err error
	p.MissingPositions, p.MissingWorkIDs, err = s.seriesMissing(ctx, p.ID, p.WorksTotal)
	return err
}

// seriesMissing returns the part numbers 1..total that none of a series' fetched works has,
// and the members known from the series page that we have not fetched.
func (s *ReadTx) seriesMissing(ctx context.Context, seriesID int64, total int) ([]int, []int64, error) {
	positions, err := queryInt64s(ctx, s.meta, `SELECT sw.position FROM series_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.series_id = ? AND w.last_fetched > 0`, seriesID)
	if err != nil {
		return nil, nil, err
	}
	have := map[int64]bool{}
	for _, pos := range positions {
		have[pos] = true
	}
	var missing []int
	for pos := 1; pos <= total; pos++ {
		if !have[int64(pos)] {
			missing = append(missing, pos)
		}
	}
	ids, err := queryInt64s(ctx, s.meta, `SELECT sw.work_id FROM series_works sw
		WHERE sw.series_id = ? AND NOT EXISTS (SELECT 1 FROM works w WHERE w.id = sw.work_id AND w.last_fetched > 0)
		ORDER BY sw.position, sw.work_id`, seriesID)
	return missing, ids, err
}

// SeriesCoverage is how much of a series the mirror holds. Content-free (ids and counts only).
type SeriesCoverage struct {
	Fetched          bool    // the series page was fetched, so WorksTotal is AO3's count
	Source           bool    // the series is itself a source, so its polls mirror it in full
	WorksTotal       int     // AO3's count (0 until Fetched)
	Mirrored         int     // works in the series we have fetched
	MissingPositions []int   // AO3 part numbers (1..WorksTotal) none of our fetched works has
	MissingWorkIDs   []int64 // series_works members we have not fetched (from the series page)
}

// Partial reports a partial series (docs/plan.md): not a source, with fewer mirrored works than
// AO3 lists. A series whose page was never fetched has no known total, so it is not partial.
// Same rule as PartialSeriesList.
func (c SeriesCoverage) Partial() bool {
	return c.Fetched && !c.Source && c.WorksTotal > c.Mirrored
}

// seriesCoverage returns a series' coverage; a series with no row has the zero value.
func (s *ReadTx) seriesCoverage(ctx context.Context, seriesID int64) (SeriesCoverage, error) {
	var c SeriesCoverage
	var fetched int64
	var source int
	err := s.meta.QueryRowContext(ctx, `SELECT se.last_fetched, se.works_count,
			(SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id = sw.work_id
			 WHERE sw.series_id = se.id AND w.last_fetched > 0),
			`+seriesIsSource("se")+`
		FROM series se WHERE se.id = ?`, seriesID).Scan(&fetched, &c.WorksTotal, &c.Mirrored, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.Fetched, c.Source = fetched > 0, source == 1
	if !c.Fetched {
		c.WorksTotal = 0
	}
	c.MissingPositions, c.MissingWorkIDs, err = s.seriesMissing(ctx, seriesID, c.WorksTotal)
	return c, err
}
