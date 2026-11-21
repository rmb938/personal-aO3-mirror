package controllers

import (
	"context"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Reads of stored collections for the API's collection views, as AO3's /collections/{name}
// pages. Every result holds collection names and titles (fanwork content): never log them.

// CollectionView is a stored collection and how many stored works are in it.
type CollectionView struct {
	Collection store.CollectionRef
	Works      int // stored works (any status) in the collection, as /collections/{id}/works lists
}

// GetCollection returns one stored collection's view, or store.ErrNoCollection. A collection
// whose works all left it (or were deleted) keeps its row, with no works.
func (c *Controllers) GetCollection(ctx context.Context, id int64) (CollectionView, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (CollectionView, error) {
		col, err := tx.CollectionByID(ctx, id)
		if err != nil {
			return CollectionView{}, err
		}
		v := CollectionView{Collection: col}
		v.Works, err = tx.CountWorks(ctx, model.WorkFilter{CollectionID: id})
		return v, err
	})
}

// ListCollectionWorks returns one page of the stored works in collectionID that match f, as
// AO3's /collections/{name}/works, and their number over all pages, or store.ErrNoCollection.
// f's collection scope is the path collection's.
func (c *Controllers) ListCollectionWorks(ctx context.Context, collectionID int64, f model.WorkFilter) (page []WorkBlurb, total int, err error) {
	f.CollectionID = collectionID
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		if _, err := tx.CollectionByID(ctx, collectionID); err != nil {
			return err
		}
		page, total, err = listWorks(ctx, tx, f)
		return err
	})
	return page, total, err
}
