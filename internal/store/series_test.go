package store

import (
	"context"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestSeriesUpsertAndNeedingInfo(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1) // in series 77, revised 2026-11-01
	s.UpsertWork(ctx, w, model.FromBlurb, 1)

	ids, _ := s.SeriesNeedingInfo(ctx, clk.t.Add(-30*24*time.Hour), 10)
	if len(ids) != 1 || ids[0] != 77 {
		t.Fatalf("never-fetched series should need info: %v", ids)
	}
	ser := model.Series{ID: 77, Title: "Fixture Series", SummaryHTML: "<p>fixture description</p>", NotesHTML: "<p>n</p>",
		Begun: "2026-01-01", Updated: "2026-11-01", Words: 1234, WorksCount: 2, Complete: false, Bookmarks: 3,
		Creators: []model.Creator{{User: "fixture_author", Pseud: "fixture_author"}}}
	if err := s.UpsertSeries(ctx, ser); err != nil {
		t.Fatal(err)
	}
	var desc string
	var words, count, creators int
	s.Meta.QueryRow(`SELECT summary_html, words, works_count FROM series WHERE id = 77`).Scan(&desc, &words, &count)
	s.Meta.QueryRow(`SELECT COUNT(*) FROM series_creators WHERE series_id = 77`).Scan(&creators)
	if desc != ser.SummaryHTML || words != 1234 || count != 2 || creators != 1 {
		t.Fatalf("stored: %q %d %d %d", desc, words, count, creators)
	}
	if ids, _ := s.SeriesNeedingInfo(ctx, clk.t.Add(-30*24*time.Hour), 10); len(ids) != 0 {
		t.Fatalf("fresh series should not need info: %v", ids)
	}
	// A member work revised after the series fetch makes it stale again.
	clk.t = clk.t.Add(time.Hour)
	w.Revised, w.ChaptersPosted = "2026-12-01", 3
	s.UpsertWork(ctx, w, model.FromBlurb, 1)
	if ids, _ := s.SeriesNeedingInfo(ctx, clk.t.Add(-30*24*time.Hour), 10); len(ids) != 1 {
		t.Fatalf("series with a newer member work should need info: %v", ids)
	}
	// Series that are sources are refreshed by their polls instead.
	addSource(t, s, "77", SourceActive, 0)
	s.Meta.Exec(`UPDATE sources SET kind = 'series' WHERE target = '77'`)
	if ids, _ := s.SeriesNeedingInfo(ctx, clk.t.Add(time.Hour), 10); len(ids) != 0 {
		t.Fatalf("source series must be excluded: %v", ids)
	}
}
