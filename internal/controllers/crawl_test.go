package controllers

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
)

// A rescan page that ends a walk with pages left over AO3's result window marks the rescan
// truncated (so its misses are not counted); a plain backfill is not marked, and a walk that
// reached the last page is not either.
func TestAdvanceSourceCursorMarksTruncatedRescan(t *testing.T) {
	c := &Controllers{options: Options{MaxResults: 20, WindowDays: 365}} // one page per walk
	log := slog.New(slog.DiscardHandler)
	day1 := &ao3url.Window{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	cases := []struct {
		name   string
		cur    SourceCursor
		win    *ao3url.Window
		listed parse.Listing
		want   bool
	}{
		{"rescan cut off", SourceCursor{NextPage: 1, Rescan: true},
			nil, parse.Listing{Total: 45, Pagination: parse.Pagination{Current: 1, Last: 3}}, true},
		{"backfill cut off", SourceCursor{NextPage: 1},
			nil, parse.Listing{Total: 45, Pagination: parse.Pagination{Current: 1, Last: 3}}, false},
		{"rescan complete", SourceCursor{NextPage: 1, Rescan: true},
			nil, parse.Listing{Total: 15, Pagination: parse.Pagination{Current: 1, Last: 1}}, false},
		{"rescan one-day window cut off", SourceCursor{NextPage: 1, Rescan: true, SpanDays: 1, WindowFrom: "2026-01-01", WindowTo: "2026-01-01"},
			day1, parse.Listing{Total: 45, Pagination: parse.Pagination{Current: 1, Last: 3}}, true},
	}
	for _, tc := range cases {
		cur := tc.cur
		c.advanceSourceCursor(&cur, 1, tc.win, tc.listed, Detail{}, log)
		require.Equal(t, tc.want, cur.RescanTruncated, tc.name)
	}
}
