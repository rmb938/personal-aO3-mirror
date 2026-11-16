package reparse

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	require.NoError(t, err)
	return b
}

func TestReparse(t *testing.T) {
	ctx := context.Background()
	clk := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }
	dir := t.TempDir()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st, err := store.Open(ctx, dir, store.Options{Now: now, Logger: log})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	putPage := func(id int64, path string, page []byte) {
		t.Helper()
		_, err := st.PutRawPage(ctx, id, path, page, 1)
		require.NoError(t, err)
	}
	upsert := func(w model.Work, origin model.Origin, pv int) {
		t.Helper()
		_, err := st.UpsertWork(ctx, w, origin, pv)
		require.NoError(t, err)
	}
	// Work 1001: stored raw page, parsed by an "old" parser (version 0), metadata stale.
	putPage(1001, "/works/1001?view_full_work=true", fixture(t, "work_chaptered.html"))
	upsert(model.Work{ID: 1001, Title: "stale"}, model.FromPage, 0)
	require.NoError(t, st.SetWorkStatus(ctx, 1001, model.StatusDeleted))
	// Work 1002: already current; Work 9: broken raw page.
	putPage(1002, "/works/1002?view_full_work=true", fixture(t, "work_oneshot.html"))
	upsert(model.Work{ID: 1002, Title: "current"}, model.FromPage, parse.Version)
	putPage(9, "/works/9", []byte(`<html><body><div id="outer"><div id="main">x</div></div></body></html>`))
	upsert(model.Work{ID: 9}, model.FromPage, 0)

	var lastFetchedBefore int64
	require.NoError(t, st.Meta.QueryRow(`SELECT last_fetched FROM works WHERE id = 1001`).Scan(&lastFetchedBefore))
	clk = clk.Add(time.Hour)

	res, err := Run(ctx, st, Options{DataDir: dir, QueueImages: true, Logger: log})
	require.NoError(t, err)
	require.Equal(t, 3, res.Seen)
	require.Equal(t, 1, res.Reparsed)
	require.Equal(t, 1, res.Skipped)
	require.Equal(t, 1, res.Failed)
	// work_chaptered.html embeds one absolute image in a chapter body.
	var queued int
	require.NoError(t, st.Meta.QueryRow(`SELECT COUNT(*) FROM work_images WHERE work_id = 1001`).Scan(&queued))
	require.Equal(t, 1, res.ImagesQueued, "reparse should queue the embedded image")
	require.Equal(t, 1, queued)

	var title, status string
	var pv int
	var lastFetched int64
	require.NoError(t, st.Meta.QueryRow(`SELECT title, status, parser_version, last_fetched FROM works WHERE id = 1001`).
		Scan(&title, &status, &pv, &lastFetched))
	require.Equal(t, "Fixture Title One", title)
	require.Equal(t, "deleted", status, "reparse must not change the work status")
	require.Equal(t, parse.Version, pv)
	require.Equal(t, lastFetchedBefore, lastFetched, "reparse is not a fetch")
	chs, err := st.ChapterBodies(ctx, 1001)
	require.NoError(t, err)
	require.Len(t, chs, 3)
	require.Contains(t, chs[0].SummaryHTML, "chapter summary")
	m, err := filepath.Glob(filepath.Join(dir, "debug", "work-9-*.skel.html"))
	require.NoError(t, err)
	require.Len(t, m, 1, "expected a debug skeleton for the broken page")

	// Newer listing blurb wins over an older page on --all.
	upsert(model.Work{ID: 1001, Title: "Newer Blurb Title", Stats: model.Stats{Kudos: 5000}}, model.FromBlurb, parse.Version)
	res, err = Run(ctx, st, Options{All: true, QueueImages: true, Logger: log})
	require.NoError(t, err)
	require.Equal(t, 0, res.ImagesQueued, "already-queued images must not be counted again")
	var kudos int
	require.NoError(t, st.Meta.QueryRow(`SELECT title, kudos FROM works WHERE id = 1001`).Scan(&title, &kudos))
	require.Equal(t, "Newer Blurb Title", title, "reparse must not roll back newer listing data")
	require.Equal(t, 5000, kudos)
	require.NotContains(t, strings.ToLower(logs.String()), "fixture title", "reparse logs leaked content")
}
