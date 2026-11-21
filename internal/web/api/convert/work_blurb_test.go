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

func TestWorkBlurb(t *testing.T) {
	fetched := time.Date(2026, 11, 1, 2, 3, 4, 0, time.FixedZone("x", 3600))
	b := controllers.WorkBlurb{
		Work: store.WorkRow{Work: model.Work{
			ID: 9, Title: "Fixture Title", Anonymous: true, Restricted: true, SummaryHTML: "<p>fixture summary</p>",
			NotesHTML: "<p>fixture notes</p>", EndNotesHTML: "<p>fixture end</p>", Language: "English", Words: 1500,
			ChaptersPosted: 2, ChaptersExpected: 3, Complete: true, Published: "2026-01-02", Revised: "2026-02-03",
			Stats: model.Stats{Kudos: 1, Hits: 2, Comments: 3, Bookmarks: 4},
		}, Status: model.StatusHidden, LastFetched: fetched},
		Creators: []store.CreatorRef{{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}},
		Tags:     []store.TagRef{{ID: 6, Name: "Fixture Fandom", Type: model.TagFandom}},
		Series:   []store.SeriesPart{{ID: 77, Title: "Fixture Series", Position: 2}},
	}
	got := WorkBlurb(b)
	assert.Equal(t, dto.WorkBlurb{
		ID: 9, Status: "hidden", Title: "Fixture Title",
		Creators:  []dto.WorkCreator{{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}},
		Anonymous: true, Restricted: true, SummaryHTML: "<p>fixture summary</p>", Language: "English", Words: 1500,
		ChaptersPosted: 2, ChaptersExpected: 3, Complete: true, PublishedOn: "2026-01-02", RevisedOn: "2026-02-03",
		Stats:         dto.WorkStats{Kudos: 1, Hits: 2, Comments: 3, Bookmarks: 4},
		Tags:          []dto.WorkTag{{ID: 6, Name: "Fixture Fandom", Type: "Fandom"}},
		Series:        []dto.WorkSeriesPart{{ID: 77, Title: "Fixture Series", Position: 2}},
		LastFetchedAt: fetched.UTC(),
	}, got, "notes are not part of a blurb")
	assert.Equal(t, time.UTC, got.LastFetchedAt.Location())

	blurb := WorkBlurb(controllers.WorkBlurb{Work: store.WorkRow{Work: model.Work{ID: 2}, Status: model.StatusOK}})
	assert.Equal(t, dto.WorkBlurb{ID: 2, Status: "ok"}, blurb, "no relations are nil (written as []), no fetch time")
}

func TestWorkBlurbList(t *testing.T) {
	got := WorkBlurbList(nil, 0, 1, 20)
	assert.Equal(t, dto.List[dto.WorkBlurb]{Page: 1, PerPage: 20}, got, "empty page: nil items")
	got = WorkBlurbList([]controllers.WorkBlurb{{Work: store.WorkRow{Work: model.Work{ID: 2}}},
		{Work: store.WorkRow{Work: model.Work{ID: 1}}}}, 5, 3, 2)
	assert.Equal(t, []int64{2, 1}, []int64{got.Items[0].ID, got.Items[1].ID})
	assert.Equal(t, 5, got.Total)
	assert.Equal(t, int64(3), got.Page)
}

// The handler passes the dto enums' values to the store as model values, so the two sets must
// match.
func TestWorkSortEnums(t *testing.T) {
	var sorts []string
	for _, s := range append(model.WorkSorts(), model.WorkSortBestMatch) {
		sorts = append(sorts, string(s))
	}
	assert.Equal(t, string(model.WorkSortBestMatch), string(dto.WorkSortBestMatch))
	assert.ElementsMatch(t, sorts, dto.WorkSort("").EnumValues())
	assert.ElementsMatch(t, []string{string(model.SortAsc), string(model.SortDesc)}, dto.SortDirection("").EnumValues())
}
