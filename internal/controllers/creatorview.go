package controllers

import (
	"context"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Reads of stored creators for the API's creator views. A creator is one AO3 pseud, as AO3's
// /users/{login}/pseuds/{pseud} page; users grouping pseuds are not modelled. Every result
// holds logins and pseuds (fanwork content): never log them.

// CreatorView is a stored creator and how many stored works it is on.
type CreatorView struct {
	Creator store.CreatorRef
	Works   int // stored works (any status) with the creator in the byline, as /creators/{id}/works lists
}

// GetCreator returns one stored creator's view, or store.ErrNoCreator. A creator named only
// by a series page has no works.
func (c *Controllers) GetCreator(ctx context.Context, id int64) (CreatorView, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (CreatorView, error) {
		cr, err := tx.CreatorByID(ctx, id)
		if err != nil {
			return CreatorView{}, err
		}
		v := CreatorView{Creator: cr}
		v.Works, err = tx.CountWorks(ctx, model.WorkFilter{CreatorID: id})
		return v, err
	})
}

// ListCreators returns one page of the stored creators matching f (f.Offset ≥ 0, 1 ≤ f.Limit
// ≤ store.MaxCreatorPage, in f's order), each with its works count, and the number of matching
// creators over all pages, read in one transaction so they agree.
func (c *Controllers) ListCreators(ctx context.Context, f model.CreatorFilter) (page []store.CreatorRow, total int, err error) {
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		if page, err = tx.FindCreators(ctx, f); err != nil {
			return err
		}
		total, err = tx.CountCreators(ctx, f)
		return err
	})
	return page, total, err
}

// ListCreatorWorks returns one page of the stored works with creatorID in the byline that
// match f, as AO3's /users/{login}/pseuds/{pseud}/works, and their number over all pages, or
// store.ErrNoCreator. f's creator scope is the path creator's.
func (c *Controllers) ListCreatorWorks(ctx context.Context, creatorID int64, f model.WorkFilter) (page []WorkBlurb, total int, err error) {
	f.CreatorID = creatorID
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		if _, err := tx.CreatorByID(ctx, creatorID); err != nil {
			return err
		}
		page, total, err = listWorks(ctx, tx, f)
		return err
	})
	return page, total, err
}
