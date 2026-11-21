package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Readers for one work's related rows, for the API's work view. Every result holds fanwork
// content (names, titles): never log it.

// CreatorRef is a stored creator: its id, AO3 login and pseud.
type CreatorRef struct {
	ID    int64
	User  string
	Pseud string
}

// CollectionRef is a stored collection: its id, URL name and title.
type CollectionRef struct {
	ID    int64
	Name  string
	Title string
}

// SeriesPart is one series a work belongs to: the series' id and title, and the work's part
// number in it.
type SeriesPart struct {
	ID       int64
	Title    string
	Position int // the work's AO3 part number (0 if unknown)
}

// WorkSeriesPart is one series a work belongs to, with how much of the series is mirrored.
type WorkSeriesPart struct {
	SeriesPart
	Coverage SeriesCoverage
}

// MaxWorkBatch bounds the work ids a batch relation reader (WorksCreators, WorksTags,
// WorksSeries) takes: one page of a work listing.
const MaxWorkBatch = 100

// WorkCreators returns a work's creators in byline order.
func (s *ReadTx) WorkCreators(ctx context.Context, workID int64) ([]CreatorRef, error) {
	m, err := s.WorksCreators(ctx, []int64{workID})
	return m[workID], err
}

// WorksCreators returns the creators of each of the given works (at most MaxWorkBatch) in
// byline order, by work id. A work with none has no entry.
func (s *ReadTx) WorksCreators(ctx context.Context, workIDs []int64) (map[int64][]CreatorRef, error) {
	return queryByWork(ctx, s.meta, workIDs, func(r *sql.Rows, work *int64, c *CreatorRef) error {
		return r.Scan(work, &c.ID, &c.User, &c.Pseud)
	}, func(in string) string {
		return `SELECT wc.work_id, c.id, c.user_login, c.pseud FROM work_creators wc
			JOIN creators c ON c.id = wc.creator_id
			WHERE wc.work_id IN (` + in + `) ORDER BY wc.work_id, wc.position, c.id`
	})
}

// WorkTags returns a work's tags in page order (AO3 groups them by type).
func (s *ReadTx) WorkTags(ctx context.Context, workID int64) ([]TagRef, error) {
	m, err := s.WorksTags(ctx, []int64{workID})
	return m[workID], err
}

// WorksTags returns the tags of each of the given works (at most MaxWorkBatch) in page order,
// by work id. A work with none has no entry.
func (s *ReadTx) WorksTags(ctx context.Context, workIDs []int64) (map[int64][]TagRef, error) {
	return queryByWork(ctx, s.meta, workIDs, func(r *sql.Rows, work *int64, t *TagRef) error {
		return r.Scan(work, &t.ID, &t.Name, &t.Type)
	}, func(in string) string {
		return `SELECT wt.work_id, t.id, t.name, t.type FROM work_tags wt JOIN tags t ON t.id = wt.tag_id
			WHERE wt.work_id IN (` + in + `) ORDER BY wt.work_id, wt.position, t.id`
	})
}

// WorksSeries returns the series each of the given works (at most MaxWorkBatch) belongs to, by
// series id, without coverage (WorkSeries has it), by work id. A work in none has no entry.
func (s *ReadTx) WorksSeries(ctx context.Context, workIDs []int64) (map[int64][]SeriesPart, error) {
	return queryByWork(ctx, s.meta, workIDs, func(r *sql.Rows, work *int64, p *SeriesPart) error {
		return r.Scan(work, &p.ID, &p.Title, &p.Position)
	}, func(in string) string {
		return `SELECT sw.work_id, se.id, se.title, sw.position FROM series_works sw
			JOIN series se ON se.id = sw.series_id
			WHERE sw.work_id IN (` + in + `) ORDER BY sw.work_id, se.id`
	})
}

// queryByWork runs the query q builds for an IN list of workIDs' bind markers (only "?,?,…" is
// concatenated; the ids are bound) and groups the rows by the work id scan reads, keeping their
// order. More than MaxWorkBatch ids is an error.
func queryByWork[T any](ctx context.Context, db querier, workIDs []int64,
	scan func(r *sql.Rows, work *int64, v *T) error, q func(in string) string,
) (map[int64][]T, error) {
	if len(workIDs) > MaxWorkBatch {
		return nil, fmt.Errorf("store: %d work ids in a batch (max %d)", len(workIDs), MaxWorkBatch)
	}
	out := make(map[int64][]T, len(workIDs))
	if len(workIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(workIDs))
	for i, id := range workIDs {
		args[i] = id
	}
	type row struct {
		work int64
		v    T
	}
	rows, err := queryRows(ctx, db, func(r *sql.Rows, x *row) error {
		return scan(r, &x.work, &x.v)
	}, q(placeholders(len(workIDs))), args...)
	if err != nil {
		return nil, err
	}
	for _, x := range rows {
		out[x.work] = append(out[x.work], x.v)
	}
	return out, nil
}

// WorkCollections returns the collections a work is in, by collection id.
func (s *ReadTx) WorkCollections(ctx context.Context, workID int64) ([]CollectionRef, error) {
	return queryRows(ctx, s.meta, func(r *sql.Rows, c *CollectionRef) error {
		return r.Scan(&c.ID, &c.Name, &c.Title)
	}, `SELECT c.id, c.name, c.title FROM work_collections wc JOIN collections c ON c.id = wc.collection_id
		WHERE wc.work_id = ? ORDER BY c.id`, workID)
}

// WorkSeries returns the series a work belongs to, by series id, each with its coverage.
func (s *ReadTx) WorkSeries(ctx context.Context, workID int64) ([]WorkSeriesPart, error) {
	m, err := s.WorksSeries(ctx, []int64{workID})
	if err != nil {
		return nil, err
	}
	var parts []WorkSeriesPart
	for _, p := range m[workID] {
		c, err := s.seriesCoverage(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		parts = append(parts, WorkSeriesPart{SeriesPart: p, Coverage: c})
	}
	return parts, nil
}

// WorkChapters returns a work's chapter list from meta.db: id, position, title and published
// date. Bodies, notes and summaries are in content.db (ChapterBodies, ChapterBody).
func (s *ReadTx) WorkChapters(ctx context.Context, workID int64) ([]model.Chapter, error) {
	return queryRows(ctx, s.meta, func(r *sql.Rows, c *model.Chapter) error {
		return r.Scan(&c.ID, &c.Position, &c.Title, &c.Published)
	}, `SELECT chapter_id, position, title, published FROM chapters WHERE work_id = ? ORDER BY position`, workID)
}

// queryRows runs q and scans each row into a new T.
func queryRows[T any](ctx context.Context, db querier, scan func(*sql.Rows, *T) error, q string, args ...any) (out []T, err error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
