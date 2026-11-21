package controllers

import (
	"context"
	"errors"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Reads of stored tags for the API's tag views. Every result holds tag names (fanwork
// content): never log them.

// TagView is a stored tag with what its AO3 tag page shows: its wrangling state, the tag it
// is a synonym of, its related tags (each grouped by type, at most store.MaxTagGroup per
// type) and how many stored works it matches.
type TagView struct {
	Tag      store.TagInfo
	Merger   *store.TagRef // the tag this one is a synonym of; nil if none
	Parents  []store.TagGroup
	Children []store.TagGroup
	MetaTags []store.TagGroup
	SubTags  []store.TagGroup
	Synonyms []store.TagGroup
	Works    int // stored works (any status) a filter on this tag matches, as /tags/{id}/works lists
}

// GetTag returns one stored tag's view by id, or store.ErrNoTag.
func (c *Controllers) GetTag(ctx context.Context, id int64) (TagView, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (TagView, error) {
		t, err := tx.TagInfoByID(ctx, id)
		if err != nil {
			return TagView{}, err
		}
		return tagView(ctx, tx, t)
	})
}

// GetTagByName returns one stored tag's view by its name (case-insensitive, as AO3's
// /tags/{name} page), or store.ErrNoTag.
func (c *Controllers) GetTagByName(ctx context.Context, name string) (TagView, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (TagView, error) {
		t, err := tx.TagByName(ctx, name)
		if err != nil {
			return TagView{}, err
		}
		return tagView(ctx, tx, t)
	})
}

// ListTags returns one page of the stored tags matching f (f.Offset ≥ 0, 1 ≤ f.Limit ≤
// store.MaxTagPage, in f's order), each with its uses, and the number of matching tags over
// all pages, read in one transaction so they agree.
func (c *Controllers) ListTags(ctx context.Context, f model.TagFilter) (page []store.TagRow, total int, err error) {
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		if page, err = tx.FindTags(ctx, f); err != nil {
			return err
		}
		total, err = tx.CountTags(ctx, f)
		return err
	})
	return page, total, err
}

// ErrNoMedium means no stored media tag has the requested id (no tag, or one of another type).
var ErrNoMedium = errors.New("controllers: no such media tag")

// MediumTopFandoms is how many fandoms ListMedia lists under each medium, as AO3's /media page.
const MediumTopFandoms = 5

// Medium is one entry of AO3's /media page: a stored media tag and its most used fandoms.
type Medium struct {
	Tag     store.TagInfo
	Fandoms []store.TagRow // at most MediumTopFandoms, most used first (ties by name)
	// FandomsTotal is every stored fandom listing this medium as a parent, as
	// ListMediumFandoms counts them.
	FandomsTotal int
}

// ListMedia returns one page of the stored media tags by name (offset ≥ 0, 1 ≤ limit ≤
// store.MaxTagPage), each with its most used fandoms, and the number of media over all pages,
// read in one transaction. AO3 lists only canonical media; the mirror never reads a medium's
// tag page, so it lists every one a fandom's tag page named.
func (c *Controllers) ListMedia(ctx context.Context, offset, limit int) (page []Medium, total int, err error) {
	media := model.TagFilter{Types: []model.TagType{model.TagMedia}, Offset: offset, Limit: limit}
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		rows, err := tx.FindTags(ctx, media)
		if err != nil {
			return err
		}
		page = make([]Medium, len(rows))
		for i, r := range rows {
			f := mediumFandoms(r.ID, model.TagFilter{Sort: model.TagSortUses, Limit: MediumTopFandoms})
			page[i].Tag = r.TagInfo
			if page[i].Fandoms, err = tx.FindTags(ctx, f); err != nil {
				return err
			}
			if page[i].FandomsTotal, err = tx.CountTags(ctx, f); err != nil {
				return err
			}
		}
		total, err = tx.CountTags(ctx, media)
		return err
	})
	return page, total, err
}

// ListMediumFandoms returns one page of the stored fandoms listing the media tag mediumID as a
// parent, as AO3's /media/{name}/fandoms, and their number over all pages, or ErrNoMedium. f
// gives the sort, direction and page (f.Offset ≥ 0, 1 ≤ f.Limit ≤ store.MaxTagPage); its other
// filters apply too, but the type and parents are the medium's. Unlike AO3 (canonical fandoms
// but in Uncategorized Fandoms), every fandom is listed: Canonical tells them apart.
func (c *Controllers) ListMediumFandoms(ctx context.Context, mediumID int64, f model.TagFilter) (page []store.TagRow, total int, err error) {
	f = mediumFandoms(mediumID, f)
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		m, err := tx.TagByID(ctx, mediumID)
		if errors.Is(err, store.ErrNoTag) || (err == nil && m.Type != model.TagMedia) {
			return ErrNoMedium
		}
		if err != nil {
			return err
		}
		if page, err = tx.FindTags(ctx, f); err != nil {
			return err
		}
		total, err = tx.CountTags(ctx, f)
		return err
	})
	return page, total, err
}

// mediumFandoms is f limited to the fandoms with mediumID as a parent.
func mediumFandoms(mediumID int64, f model.TagFilter) model.TagFilter {
	f.Types = []model.TagType{model.TagFandom}
	f.ParentIDs = []int64{mediumID}
	return f
}

// tagView reads the rest of t's view in the caller's transaction.
func tagView(ctx context.Context, tx *store.ReadTx, t store.TagInfo) (TagView, error) {
	v := TagView{Tag: t}
	if t.MergerID != 0 {
		m, err := tx.TagByID(ctx, t.MergerID)
		if err != nil {
			return TagView{}, err
		}
		v.Merger = &m
	}
	for _, r := range []struct {
		rel store.TagRelation
		dst *[]store.TagGroup
	}{
		{store.TagParents, &v.Parents},
		{store.TagChildren, &v.Children},
		{store.TagMetaTags, &v.MetaTags},
		{store.TagSubTags, &v.SubTags},
		{store.TagSynonyms, &v.Synonyms},
	} {
		groups, err := tx.RelatedTags(ctx, t.ID, r.rel, store.MaxTagGroup)
		if err != nil {
			return TagView{}, err
		}
		*r.dst = groups
	}
	var err error
	v.Works, err = tx.CountWorks(ctx, model.WorkFilter{Tags: []int64{t.ID}})
	return v, err
}
