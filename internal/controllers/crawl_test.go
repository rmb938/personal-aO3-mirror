package controllers

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
)

// A rescan whose last page shows the walk did not reach every work (pages left over AO3's
// result window, or a total the walked pages can't hold) is marked incomplete, so its misses
// are not counted. A plain backfill is never marked, and a walk that reached the end is not.
func TestAdvanceSourceCursorMarksIncompleteRescan(t *testing.T) {
	c := &Controllers{options: Options{MaxResults: 20, WindowDays: 365}} // one page per walk
	log := slog.New(slog.DiscardHandler)
	day1 := &ao3url.Window{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	rescan := SourceCursor{NextPage: 1, Rescan: true}
	oneDay := SourceCursor{NextPage: 1, Rescan: true, SpanDays: 1, WindowFrom: "2026-01-01", WindowTo: "2026-01-01"}
	pages := func(cur, last int) parse.Pagination { return parse.Pagination{Current: cur, Last: last} }
	cases := []struct {
		name   string
		cur    SourceCursor
		win    *ao3url.Window
		listed parse.Listing
		want   bool
	}{
		{"rescan cut off at result window", rescan, nil, parse.Listing{Total: 45, Pagination: pages(1, 3)}, true},
		{"backfill cut off", SourceCursor{NextPage: 1}, nil, parse.Listing{Total: 45, Pagination: pages(1, 3)}, false},
		{"rescan complete", rescan, nil, parse.Listing{Total: 15, Pagination: pages(1, 1)}, false},
		{"rescan complete, full last page", rescan, nil, parse.Listing{Total: 20, Pagination: pages(1, 1)}, false},
		{"rescan without a total", rescan, nil, parse.Listing{Total: -1, Pagination: pages(1, 1)}, false},
		{"rescan, pagination not parsed", rescan, nil, parse.Listing{Total: 45, Pagination: pages(1, 1)}, true},
		{"backfill, pagination not parsed", SourceCursor{NextPage: 1}, nil, parse.Listing{Total: 45, Pagination: pages(1, 1)}, false},
		{"rescan one-day window cut off", oneDay, day1, parse.Listing{Total: 45, Pagination: pages(1, 3)}, true},
		{"rescan window, pagination not parsed", oneDay, day1, parse.Listing{Total: 45, Pagination: pages(1, 1)}, true},
		{"rescan window complete", oneDay, day1, parse.Listing{Total: 7, Pagination: pages(1, 1)}, false},
	}
	for _, tc := range cases {
		cur := tc.cur
		c.advanceSourceCursor(&cur, 1, tc.win, tc.listed, &BackfillDetail{}, log)
		require.Equal(t, tc.want, cur.RescanIncomplete, tc.name)
	}
}
