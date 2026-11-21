package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// seedViewTags stores works 1 (Fixture Character) and 2 (Fixture Char Syn, a synonym of it)
// and tag info: Fixture Character has parent Fixture Fandom and meta tag Fixture Char Meta.
func seedViewTags(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		for id, name := range map[int64]string{1: "Fixture Character", 2: "Fixture Char Syn"} {
			w := model.Work{ID: id, Title: "Fixture Title", Revised: "2026-11-01", Tags: []model.Tag{
				{Type: model.TagFandom, Name: "Fixture Fandom"}, {Type: model.TagCharacter, Name: name},
			}}
			if _, err := tx.UpsertWork(ctx, w, model.FromPage, 1); err != nil {
				return err
			}
		}
		char, err := tx.TagByName(ctx, "Fixture Character")
		if err != nil {
			return err
		}
		return tx.UpdateTagInfo(ctx, char.ID, store.TagInfoUpdate{Type: model.TagCharacter, Canonical: true,
			Parents: []string{"Fixture Fandom"}, MetaTags: []string{"Fixture Char Meta"},
			Synonyms: []string{"Fixture Char Syn"}})
	}))
}

// names is the tag names of groups, in order.
func names(groups []store.TagGroup) []string {
	var out []string
	for _, g := range groups {
		for _, r := range g.Tags {
			out = append(out, r.Name)
		}
	}
	return out
}

func TestGetTag(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	seedViewTags(t, e)

	v, err := e.svc.GetTagByName(ctx, "fixture character")
	require.NoError(t, err, "name lookup ignores case")
	assert.Equal(t, "Fixture Character", v.Tag.Name)
	assert.True(t, v.Tag.Canonical)
	assert.Nil(t, v.Merger)
	assert.Equal(t, []string{"Fixture Fandom"}, names(v.Parents))
	assert.Empty(t, v.Children)
	assert.Equal(t, []string{"Fixture Char Meta"}, names(v.MetaTags))
	assert.Empty(t, v.SubTags)
	assert.Equal(t, []string{"Fixture Char Syn"}, names(v.Synonyms))
	assert.Equal(t, 2, v.Works, "its works and its synonym's")

	byID, err := e.svc.GetTag(ctx, v.Tag.ID)
	require.NoError(t, err)
	assert.Equal(t, v, byID)

	syn, err := e.svc.GetTag(ctx, v.Synonyms[0].Tags[0].ID)
	require.NoError(t, err)
	require.NotNil(t, syn.Merger)
	assert.Equal(t, v.Tag.TagRef, *syn.Merger)
	assert.Equal(t, 1, syn.Works, "a synonym matches only its own works")

	fandom, err := e.svc.GetTagByName(ctx, "Fixture Fandom")
	require.NoError(t, err)
	assert.Equal(t, []string{"Fixture Character"}, names(fandom.Children))
	assert.Equal(t, 2, fandom.Works)

	_, err = e.svc.GetTag(ctx, 999_999)
	require.ErrorIs(t, err, store.ErrNoTag)
	_, err = e.svc.GetTagByName(ctx, "Fixture Missing")
	require.ErrorIs(t, err, store.ErrNoTag)
}
