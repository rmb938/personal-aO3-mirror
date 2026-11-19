package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestSource(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, loc)
	cur := controllers.SourceCursor{NextPage: 7, Order: "kudos_desc", WindowFrom: "2020-01-01", WindowTo: "2020-12-31",
		SpanDays: 30, Rescan: true, RescanStarted: 1700000000, BlockedParser: 3, RetryAt: 1700000100}
	s := model.Source{
		ID: 4, Kind: "tag", Target: "fixture tag", FiltersJSON: `{"b":["2","1"],"a":["x"]}`, Path: "/tags/x/works",
		State: model.SourceActive, BackfillOrder: "kudos_desc", PollInterval: 90 * time.Minute, Priority: 2,
		TotalWorks: 10, EstRequests: -1, BackfillCursor: cur.JSON(), CreatedAt: created, LastError: "poll page 2: not_found",
		MemberWorks: 5, FetchedWorks: 3,
	}
	got := Source(s)
	ten := 10
	assert.Equal(t, dto.Source{
		ID: 4, Kind: "tag", Target: "fixture tag",
		Filters: []dto.Filter{{Name: "a", Values: []string{"x"}}, {Name: "b", Values: []string{"2", "1"}}},
		Path:    "/tags/x/works", State: "active", Order: "kudos_desc", PollIntervalS: 5400, Priority: 2,
		TotalWorks: &ten, EstRequests: nil, MemberWorks: 5, FetchedWorks: 3,
		Backfill: &dto.Backfill{NextPage: 7, Order: "kudos_desc", WindowStartOn: "2020-01-01", WindowEndOn: "2020-12-31",
			Rescan: true, RescanStartedAt: time.Unix(1700000000, 0).UTC(), BlockedParser: 3,
			RetryAt: time.Unix(1700000100, 0).UTC()},
		CreatedAt: created.UTC(), LastError: "poll page 2: not_found",
	}, got)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
}

func TestSourceEdgeCases(t *testing.T) {
	for _, c := range []struct {
		name     string
		in       model.Source
		targetID int64
	}{
		{"series", model.Source{Kind: "series", Target: "12"}, 12},
		{"work", model.Source{Kind: "work", Target: "34"}, 34},
		{"tag with numeric name", model.Source{Kind: "tag", Target: "56"}, 0},
		{"bad series target", model.Source{Kind: "series", Target: "x"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Source(c.in)
			assert.Equal(t, c.targetID, got.TargetID)
			assert.Equal(t, []dto.Filter{}, got.Filters, "no filters is [] (not null)")
			assert.Nil(t, got.Backfill)
			require.NotNil(t, got.TotalWorks, "0 is known (only -1 is null)")
			assert.Equal(t, 0, *got.TotalWorks)
		})
	}
}

func TestSourceUnreadableColumns(t *testing.T) {
	got := Source(model.Source{FiltersJSON: "not json", BackfillCursor: "not json", TotalWorks: -1})
	assert.Equal(t, []dto.Filter{}, got.Filters)
	assert.Nil(t, got.Backfill)
	assert.Nil(t, got.TotalWorks)
	got = Source(model.Source{BackfillCursor: "{}"})
	assert.Nil(t, got.Backfill)
}

func TestSourceList(t *testing.T) {
	got := SourceList(nil, 0, 1, 20)
	assert.Equal(t, dto.List[dto.Source]{Items: []dto.Source{}, Page: 1, PerPage: 20}, got, "empty page is items: []")
	got = SourceList([]model.Source{{ID: 1}, {ID: 2}}, 7, 2, 2)
	assert.Len(t, got.Items, 2)
	assert.Equal(t, 7, got.Total)
}
