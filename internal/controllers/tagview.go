package controllers

import (
	"context"

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
