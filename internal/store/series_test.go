package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestPartialSeries(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	// Fetched parts 1 and 3 of a 4-part series; work 900 is only a blurb (never fetched).
	w1 := fixtureWork(1)
	w3 := fixtureWork(3)
	w3.Series = []model.SeriesPart{{ID: 77, Position: 3}}
	s.UpsertWork(ctx, w1, model.FromPage, 1)
	s.UpsertWork(ctx, w3, model.FromPage, 1)
	s.UpsertWork(ctx, fixtureWork(900), model.FromBlurb, 1)
	if ps, _ := s.PartialSeriesList(ctx, 10); len(ps) != 0 {
		t.Fatalf("series without info (unknown total) must not be listed: %+v", ps)
	}
	s.UpsertSeries(ctx, model.Series{ID: 77, WorksCount: 4})
	members := func(ids ...int64) []SeriesMember {
		out := make([]SeriesMember, len(ids))
		for i, id := range ids {
			out[i] = SeriesMember{WorkID: id, Position: int(id)}
		}
		return out
	}
	if err := s.SetSeriesMembers(ctx, 77, members(1, 2, 3, 4)); err != nil {
		t.Fatal(err)
	}
	ps, err := s.PartialSeriesList(ctx, 10)
	if err != nil || len(ps) != 1 {
		t.Fatalf("partial series: %+v %v", ps, err)
	}
	p := ps[0]
	if p.ID != 77 || p.WorksTotal != 4 || p.Mirrored != 2 ||
		fmt.Sprint(p.MissingPositions) != "[2 4]" || fmt.Sprint(p.MissingWorkIDs) != "[2 4]" {
		t.Fatalf("got %+v", p)
	}
	// Re-seeing the page replaces unfetched members and keeps fetched ones.
	s.SetSeriesMembers(ctx, 77, members(1, 3, 5))
	if ps, _ := s.PartialSeriesList(ctx, 10); fmt.Sprint(ps[0].MissingWorkIDs) != "[5]" || ps[0].Mirrored != 2 {
		t.Fatalf("members not replaced: %+v", ps[0])
	}
	// Unfetched members never count as mirrored, and other series_works users ignore them
	// (joins to works drop rows with no works row).
	var n int
	s.Meta.QueryRow(`SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id = sw.work_id WHERE sw.series_id = 77`).Scan(&n)
	if n != 2 {
		t.Fatalf("joined series_works = %d, want 2", n)
	}
	// A series that is a source is mirrored in full by its own polls, so it is not partial.
	addSource(t, s, "77", SourceActive, 0)
	s.Meta.Exec(`UPDATE sources SET kind = 'series' WHERE target = '77'`)
	if ps, _ := s.PartialSeriesList(ctx, 10); len(ps) != 0 {
		t.Fatalf("source series listed as partial: %+v", ps)
	}
}

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
