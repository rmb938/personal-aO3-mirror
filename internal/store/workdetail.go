package store

import (
	"context"
	"database/sql"
	"errors"

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

// WorkSeriesPart is one series a work belongs to, with how much of the series is mirrored.
type WorkSeriesPart struct {
	ID       int64
	Title    string
	Position int // the work's AO3 part number (0 if unknown)
	Coverage SeriesCoverage
}

// WorkCreators returns a work's creators in byline order.
func (s *ReadTx) WorkCreators(ctx context.Context, workID int64) ([]CreatorRef, error) {
	return queryRows(ctx, s.meta, func(r *sql.Rows, c *CreatorRef) error {
		return r.Scan(&c.ID, &c.User, &c.Pseud)
	}, `SELECT c.id, c.user_login, c.pseud FROM work_creators wc JOIN creators c ON c.id = wc.creator_id
		WHERE wc.work_id = ? ORDER BY wc.position, c.id`, workID)
}

// WorkTags returns a work's tags in page order (AO3 groups them by type).
func (s *ReadTx) WorkTags(ctx context.Context, workID int64) ([]TagRef, error) {
	return queryRows(ctx, s.meta, func(r *sql.Rows, t *TagRef) error {
		return r.Scan(&t.ID, &t.Name, &t.Type)
	}, `SELECT t.id, t.name, t.type FROM work_tags wt JOIN tags t ON t.id = wt.tag_id
		WHERE wt.work_id = ? ORDER BY wt.position, t.id`, workID)
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
	parts, err := queryRows(ctx, s.meta, func(r *sql.Rows, p *WorkSeriesPart) error {
		return r.Scan(&p.ID, &p.Title, &p.Position)
	}, `SELECT se.id, se.title, sw.position FROM series_works sw JOIN series se ON se.id = sw.series_id
		WHERE sw.work_id = ? ORDER BY se.id`, workID)
	if err != nil {
		return nil, err
	}
	for i := range parts {
		if parts[i].Coverage, err = s.seriesCoverage(ctx, parts[i].ID); err != nil {
			return nil, err
		}
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
