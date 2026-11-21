package controllers

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Reads of stored works for the API's work views. Every result holds fanwork content: never
// log it. Each read is one read transaction over both databases, so the meta.db rows and the
// content.db bodies come from one snapshot; they are merged here (the databases are not
// attached).

// WorkView is a stored work with its relations and chapter list, without bodies.
type WorkView struct {
	Work        store.WorkRow // the works row; its relation slices are left empty
	Creators    []store.CreatorRef
	Tags        []store.TagRef
	Series      []store.WorkSeriesPart
	Collections []store.CollectionRef
	Chapters    []model.Chapter // id, position, title, published date only
}

// ListWorks returns one page of the works matching f (f.Offset ≥ 0, f.Limit ≥ 1, in f's order)
// and the number of matching works over all pages, read in one transaction so they agree. The
// rows' relation slices are left empty.
func (c *Controllers) ListWorks(ctx context.Context, f model.WorkFilter) (page []store.WorkRow, total int, err error) {
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if page, err = tx.FindWorks(ctx, f); err != nil {
			return err
		}
		total, err = tx.CountWorks(ctx, f)
		return err
	})
	return page, total, err
}

// GetWork returns one stored work with its creators, tags, series (with coverage),
// collections and chapter list, or store.ErrNoWork.
func (c *Controllers) GetWork(ctx context.Context, id int64) (WorkView, error) {
	var v WorkView
	err := c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if v.Work, err = tx.Work(ctx, id); err != nil {
			return err
		}
		if v.Creators, err = tx.WorkCreators(ctx, id); err != nil {
			return err
		}
		if v.Tags, err = tx.WorkTags(ctx, id); err != nil {
			return err
		}
		if v.Series, err = tx.WorkSeries(ctx, id); err != nil {
			return err
		}
		if v.Collections, err = tx.WorkCollections(ctx, id); err != nil {
			return err
		}
		v.Chapters, err = tx.WorkChapters(ctx, id)
		return err
	})
	return v, err
}

// GetChapter returns one chapter of a stored work, its body, notes, end notes and summary
// with its title and published date. It returns store.ErrNoWork for an unknown work and
// store.ErrNoChapter when no body is stored at that position (a work known only from listing
// blurbs has none).
func (c *Controllers) GetChapter(ctx context.Context, workID int64, position int) (model.Chapter, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (model.Chapter, error) {
		if err := workExists(ctx, tx, workID); err != nil {
			return model.Chapter{}, err
		}
		body, err := tx.ChapterBody(ctx, workID, position)
		if err != nil {
			return model.Chapter{}, err
		}
		meta, err := tx.WorkChapters(ctx, workID)
		if err != nil {
			return model.Chapter{}, err
		}
		for _, m := range meta {
			if m.Position == position {
				return mergeChapter(m, body), nil
			}
		}
		return body, nil
	})
}

// MaxWorkChaptersBytes caps GetWorkChapters: the uncompressed size of a work's chapter bodies,
// notes, end notes and summaries together. A larger work is read one chapter at a time.
const MaxWorkChaptersBytes = 16 << 20

// ErrWorkTooLarge means a work's chapters are over MaxWorkChaptersBytes together.
var ErrWorkTooLarge = errors.New("controllers: work chapters over the size cap")

// GetWorkChapters returns every chapter of a stored work with bodies, in order, or
// store.ErrNoWork. A work known only from listing blurbs has none. It returns ErrWorkTooLarge,
// before loading any body, when the chapters are over MaxWorkChaptersBytes.
func (c *Controllers) GetWorkChapters(ctx context.Context, id int64) ([]model.Chapter, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) ([]model.Chapter, error) {
		if err := workExists(ctx, tx, id); err != nil {
			return nil, err
		}
		size, err := tx.ChapterContentBytes(ctx, id)
		if err != nil {
			return nil, err
		}
		if size > MaxWorkChaptersBytes {
			return nil, ErrWorkTooLarge
		}
		meta, err := tx.WorkChapters(ctx, id)
		if err != nil {
			return nil, err
		}
		bodies, err := tx.ChapterBodies(ctx, id)
		if err != nil {
			return nil, err
		}
		return mergeChapters(meta, bodies), nil
	})
}

// GetWorkSkin returns a stored work's skin CSS ("" if it has none), or store.ErrNoWork.
func (c *Controllers) GetWorkSkin(ctx context.Context, id int64) (string, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (string, error) {
		if err := workExists(ctx, tx, id); err != nil {
			return "", err
		}
		return tx.WorkSkin(ctx, id)
	})
}

// workExists returns store.ErrNoWork unless the work has a row.
func workExists(ctx context.Context, tx *store.ReadTx, id int64) error {
	ok, err := tx.WorkExists(ctx, id)
	if err == nil && !ok {
		err = store.ErrNoWork
	}
	return err
}

// mergeChapters joins a work's chapter list (meta.db) with its bodies (content.db) by
// position. Both are written by the same page fetch, but content.db commits first, so after a
// failure between the commits the two can differ: every position in either is kept, a chapter
// with no stored body has empty content, and one with no list row has no title or date.
func mergeChapters(meta, bodies []model.Chapter) []model.Chapter {
	byPos := make(map[int]model.Chapter, len(meta)+len(bodies))
	for _, m := range meta {
		byPos[m.Position] = m
	}
	for _, b := range bodies {
		if m, ok := byPos[b.Position]; ok {
			byPos[b.Position] = mergeChapter(m, b)
		} else {
			byPos[b.Position] = b
		}
	}
	out := make([]model.Chapter, 0, len(byPos))
	for _, c := range byPos {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b model.Chapter) int { return cmp.Compare(a.Position, b.Position) })
	return out
}

// mergeChapter is body with the title and published date of its chapter list row m. The
// list's chapter id wins when it has one.
func mergeChapter(m, body model.Chapter) model.Chapter {
	body.Title, body.Published = m.Title, m.Published
	if m.ID != 0 {
		body.ID = m.ID
	}
	return body
}
