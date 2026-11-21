package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestWorkView(t *testing.T) {
	fetched := time.Date(2026, 11, 1, 2, 3, 4, 0, time.FixedZone("x", 3600))
	v := controllers.WorkView{
		Work: store.WorkRow{Work: model.Work{
			ID: 9, Title: "Fixture Title", Anonymous: true, Restricted: true, SummaryHTML: "<p>fixture summary</p>",
			NotesHTML: "<p>fixture notes</p>", EndNotesHTML: "<p>fixture end</p>", Language: "English", Words: 1500,
			ChaptersPosted: 2, ChaptersExpected: 3, Complete: false, Published: "2026-01-02", Revised: "2026-02-03",
			Stats: model.Stats{Kudos: 1, Hits: 2, Comments: 3, Bookmarks: 4},
		}, Status: model.StatusOK, LastFetched: fetched},
		Creators:    []store.CreatorRef{{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}},
		Tags:        []store.TagRef{{ID: 6, Name: "Fixture Fandom", Type: model.TagFandom}},
		Series:      []store.WorkSeriesPart{{ID: 77, Title: "Fixture Series", Position: 2}},
		Collections: []store.CollectionRef{{ID: 8, Name: "fixture_coll", Title: "Fixture Coll"}},
		Chapters:    []model.Chapter{{ID: 11, Position: 1, Title: "Fixture Chapter", Published: "2026-01-02"}},
	}
	got := WorkView(v)
	assert.Equal(t, dto.Work{
		ID: 9, Status: "ok", Title: "Fixture Title",
		Creators:  []dto.WorkCreator{{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}},
		Anonymous: true, Restricted: true, SummaryHTML: "<p>fixture summary</p>", NotesHTML: "<p>fixture notes</p>",
		EndNotesHTML: "<p>fixture end</p>", Language: "English", Words: 1500, ChaptersPosted: 2, ChaptersExpected: 3,
		PublishedOn: "2026-01-02", RevisedOn: "2026-02-03",
		Stats:         dto.WorkStats{Kudos: 1, Hits: 2, Comments: 3, Bookmarks: 4},
		Tags:          []dto.WorkTag{{ID: 6, Name: "Fixture Fandom", Type: "Fandom"}},
		Series:        []dto.WorkSeries{{ID: 77, Title: "Fixture Series", Position: 2, CompleteLocally: true}},
		Collections:   []dto.WorkCollection{{ID: 8, Name: "fixture_coll", Title: "Fixture Coll"}},
		Chapters:      []dto.ChapterRef{{ID: 11, Position: 1, Title: "Fixture Chapter", PublishedOn: "2026-01-02"}},
		LastFetchedAt: fetched.UTC(),
	}, got)
	assert.Equal(t, time.UTC, got.LastFetchedAt.Location())

	blurb := WorkView(controllers.WorkView{Work: store.WorkRow{Work: model.Work{ID: 2}, Status: model.StatusDeleted}})
	assert.Equal(t, dto.Work{ID: 2, Status: "deleted"}, blurb, "no relations are nil (written as []), no fetch time")
}

func TestWorkSeries(t *testing.T) {
	for _, c := range []struct {
		name string
		in   store.SeriesCoverage
		want dto.WorkSeries
	}{
		{"never fetched: total unknown, not partial", store.SeriesCoverage{Mirrored: 1},
			dto.WorkSeries{CompleteLocally: true, WorksMirrored: 1}},
		{"partial", store.SeriesCoverage{Fetched: true, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{1, 3},
			MissingWorkIDs: []int64{40}},
			dto.WorkSeries{WorksTotal: new(3), WorksMirrored: 1, MissingPositions: []int{1, 3}, MissingWorkIDs: []int64{40}}},
		{"complete", store.SeriesCoverage{Fetched: true, WorksTotal: 2, Mirrored: 2},
			dto.WorkSeries{CompleteLocally: true, WorksTotal: new(2), WorksMirrored: 2}},
		{"a source is never partial", store.SeriesCoverage{Fetched: true, Source: true, WorksTotal: 3, Mirrored: 1},
			dto.WorkSeries{CompleteLocally: true, WorksTotal: new(3), WorksMirrored: 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.want.ID, c.want.Title, c.want.Position = 7, "Fixture Series", 0
			assert.Equal(t, c.want, workSeries(store.WorkSeriesPart{ID: 7, Title: "Fixture Series", Coverage: c.in}))
		})
	}
}

func TestWorkSkin(t *testing.T) {
	assert.Equal(t, dto.WorkSkin{CSS: "#workskin p { color: red; }"}, WorkSkin("#workskin p { color: red; }"))
	assert.Equal(t, dto.WorkSkin{}, WorkSkin(""))
}
