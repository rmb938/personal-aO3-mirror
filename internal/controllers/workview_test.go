package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// seedViewWork stores work 1 as a fetched page (two chapters with bodies, a skin) and work 2
// from a listing blurb only.
func seedViewWork(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	w := model.Work{
		ID: 1, Title: "Fixture Title View", Language: "English", Words: 2000, ChaptersPosted: 2, Revised: "2026-11-01",
		Creators:    []model.Creator{{User: "fixture_author", Pseud: "fixture_pseud"}},
		Tags:        []model.Tag{{Type: model.TagRating, Name: "Fixture Rating"}, {Type: model.TagFandom, Name: "Fixture Fandom"}},
		Series:      []model.SeriesPart{{ID: 77, Title: "Fixture Series", Position: 2}},
		Collections: []model.Collection{{Name: "fixture_coll", Title: "Fixture Coll"}},
		Chapters: []model.Chapter{
			{ID: 11, Position: 1, Title: "Fixture Chapter One", Published: "2026-10-01", BodyHTML: "<p>fixture one</p>"},
			{ID: 12, Position: 2, Title: "Fixture Chapter Two", Published: "2026-11-01", BodyHTML: "<p>fixture two</p>",
				EndNotesHTML: "<p>fixture end</p>"},
		},
		SkinCSS: "#workskin p { color: red; }",
	}
	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.PutWorkContent(ctx, w.ID, w.Chapters, w.SkinCSS); err != nil {
			return err
		}
		if _, err := tx.UpsertWork(ctx, w, model.FromPage, 1); err != nil {
			return err
		}
		_, err := tx.UpsertWork(ctx, model.Work{ID: 2, Title: "Fixture Blurb", Revised: "2026-11-01"}, model.FromBlurb, 1)
		return err
	}))
}

func TestGetWork(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	seedViewWork(t, e)

	v, err := e.svc.GetWork(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "Fixture Title View", v.Work.Title)
	assert.False(t, v.Work.LastFetched.IsZero())
	require.Len(t, v.Creators, 1)
	assert.Equal(t, "fixture_pseud", v.Creators[0].Pseud)
	require.Len(t, v.Tags, 2)
	assert.Equal(t, model.TagFandom, v.Tags[1].Type)
	require.Len(t, v.Series, 1)
	assert.Equal(t, 2, v.Series[0].Position)
	assert.Equal(t, 1, v.Series[0].Coverage.Mirrored)
	require.Len(t, v.Collections, 1)
	require.Len(t, v.Chapters, 2)
	assert.Equal(t, "Fixture Chapter Two", v.Chapters[1].Title)
	assert.Empty(t, v.Chapters[1].BodyHTML, "the work view has no bodies")

	blurb, err := e.svc.GetWork(ctx, 2)
	require.NoError(t, err)
	assert.True(t, blurb.Work.LastFetched.IsZero(), "blurb-only work is not fetched")
	assert.Empty(t, blurb.Chapters)

	_, err = e.svc.GetWork(ctx, 3)
	require.ErrorIs(t, err, store.ErrNoWork)
}

func TestGetChapterAndSkin(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	seedViewWork(t, e)

	c, err := e.svc.GetChapter(ctx, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, model.Chapter{ID: 12, Position: 2, Title: "Fixture Chapter Two", Published: "2026-11-01",
		BodyHTML: "<p>fixture two</p>", EndNotesHTML: "<p>fixture end</p>"}, c, "body merged with its title and date")
	_, err = e.svc.GetChapter(ctx, 1, 3)
	require.ErrorIs(t, err, store.ErrNoChapter)
	_, err = e.svc.GetChapter(ctx, 2, 1)
	require.ErrorIs(t, err, store.ErrNoChapter, "blurb-only work has no bodies")
	_, err = e.svc.GetChapter(ctx, 3, 1)
	require.ErrorIs(t, err, store.ErrNoWork)

	all, err := e.svc.GetWorkChapters(ctx, 1)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "Fixture Chapter One", all[0].Title)
	assert.Equal(t, "<p>fixture one</p>", all[0].BodyHTML)
	all, err = e.svc.GetWorkChapters(ctx, 2)
	require.NoError(t, err)
	assert.Empty(t, all)
	_, err = e.svc.GetWorkChapters(ctx, 3)
	require.ErrorIs(t, err, store.ErrNoWork)

	skin, err := e.svc.GetWorkSkin(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "#workskin p { color: red; }", skin)
	skin, err = e.svc.GetWorkSkin(ctx, 2)
	require.NoError(t, err)
	assert.Empty(t, skin)
	_, err = e.svc.GetWorkSkin(ctx, 3)
	require.ErrorIs(t, err, store.ErrNoWork)
}

// A chapter list and bodies that disagree (content.db committed, meta.db did not) merge to
// every position in either, in order.
func TestMergeChapters(t *testing.T) {
	meta := []model.Chapter{{ID: 11, Position: 1, Title: "Fixture One"}, {Position: 3, Title: "Fixture Three"}}
	bodies := []model.Chapter{{ID: 21, Position: 2, BodyHTML: "<p>two</p>"}, {ID: 23, Position: 3, BodyHTML: "<p>three</p>"},
		{Position: 1, BodyHTML: "<p>one</p>"}}
	assert.Equal(t, []model.Chapter{
		{ID: 11, Position: 1, Title: "Fixture One", BodyHTML: "<p>one</p>"},
		{ID: 21, Position: 2, BodyHTML: "<p>two</p>"},
		{ID: 23, Position: 3, Title: "Fixture Three", BodyHTML: "<p>three</p>"},
	}, mergeChapters(meta, bodies))
	assert.Empty(t, mergeChapters(nil, nil))
}
