package controllers

import (
	"context"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Reads of stored series for the API's series views. Every result holds fanwork content
// (titles, descriptions, names): never log it.

// SeriesView is a stored series with what its AO3 series page shows, plus how much of it the
// mirror holds.
type SeriesView struct {
	Series   store.SeriesRow // its Creators slice is left empty; Creators has them
	Creators []store.CreatorRef
	Coverage store.SeriesCoverage
	// Works is the series' stored works (fetched or known from blurbs only), as
	// /series/{id}/works lists them. Coverage.Mirrored counts only the fetched ones.
	Works int
}

// GetSeries returns one stored series' view, or store.ErrNoSeries. A series named by a work
// page but never fetched has a row with only its title, and no creators.
func (c *Controllers) GetSeries(ctx context.Context, id int64) (SeriesView, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (SeriesView, error) {
		var v SeriesView
		var err error
		if v.Series, err = tx.Series(ctx, id); err != nil {
			return SeriesView{}, err
		}
		if v.Creators, err = tx.SeriesCreators(ctx, id); err != nil {
			return SeriesView{}, err
		}
		if v.Coverage, err = tx.SeriesCoverage(ctx, id); err != nil {
			return SeriesView{}, err
		}
		v.Works, err = tx.CountSeriesWorks(ctx, id)
		return v, err
	})
}

// ListSeriesWorks returns one page of a stored series' works in part order (offset ≥ 0, 1 ≤
// limit ≤ store.MaxWorkBatch), as AO3's series page, with their blurb relations, and the
// number of its stored works over all pages, read in one transaction; or store.ErrNoSeries.
// Members the mirror does not hold are not listed (GetSeries' coverage reports them).
func (c *Controllers) ListSeriesWorks(ctx context.Context, seriesID int64, offset, limit int) (page []WorkBlurb, total int, err error) {
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		if _, err := tx.Series(ctx, seriesID); err != nil {
			return err
		}
		rows, err := tx.SeriesWorks(ctx, seriesID, offset, limit)
		if err != nil {
			return err
		}
		if page, err = workBlurbs(ctx, tx, rows); err != nil {
			return err
		}
		total, err = tx.CountSeriesWorks(ctx, seriesID)
		return err
	})
	return page, total, err
}
