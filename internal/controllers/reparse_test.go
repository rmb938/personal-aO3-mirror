package controllers

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

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
	db := st.AutoCommitForTestOnly()

	putPage := func(id int64, path string, page []byte) {
		t.Helper()
		_, err := db.PutRawPage(ctx, id, path, page, 1)
		require.NoError(t, err)
	}
	upsert := func(w model.Work, origin model.Origin, pv int) {
		t.Helper()
		_, err := db.UpsertWork(ctx, w, origin, pv)
		require.NoError(t, err)
	}
	// Work 1001: stored raw page, parsed by an "old" parser (version 0), metadata stale.
	putPage(1001, "/works/1001?view_full_work=true", []byte(fixture(t, "work_chaptered.html")))
	upsert(model.Work{ID: 1001, Title: "stale"}, model.FromPage, 0)
	require.NoError(t, db.SetWorkStatus(ctx, 1001, model.StatusDeleted))
	// Work 1002: already current; Work 9: broken raw page.
	putPage(1002, "/works/1002?view_full_work=true", []byte(fixture(t, "work_oneshot.html")))
	upsert(model.Work{ID: 1002, Title: "current"}, model.FromPage, parse.Version)
	putPage(9, "/works/9", []byte(`<html><body><div id="outer"><div id="main">x</div></div></body></html>`))
	upsert(model.Work{ID: 9}, model.FromPage, 0)

	diag := func(id int64) store.WorkDiag {
		t.Helper()
		d, err := db.WorkDiag(ctx, id)
		require.NoError(t, err)
		return d
	}
	work := func(id int64) model.Work {
		t.Helper()
		w, err := db.Work(ctx, id)
		require.NoError(t, err)
		return w
	}
	lastFetchedBefore := diag(1001).LastFetched
	clk = clk.Add(time.Hour)

	// No Client: reparse is offline, so any AO3 fetch would fail with ErrOffline.
	ctl := New(Options{Store: st, DataDir: dir, QueueImages: true, Logger: log, Now: now})
	res, err := ctl.Reparse(ctx, ReparseOptions{})
	require.NoError(t, err)
	require.Equal(t, 3, res.Seen)
	require.Equal(t, 1, res.Reparsed)
	require.Equal(t, 1, res.Skipped)
	require.Equal(t, 1, res.Failed)
	// work_chaptered.html embeds one absolute image in a chapter body.
	d := diag(1001)
	require.Equal(t, 1, res.ImagesQueued, "reparse should queue the embedded image")
	require.Equal(t, 1, d.Images)

	require.Equal(t, "Fixture Title One", work(1001).Title)
	require.Equal(t, model.StatusDeleted, d.Status, "reparse must not change the work status")
	require.Equal(t, parse.Version, d.ParserVersion)
	require.Equal(t, lastFetchedBefore, d.LastFetched, "reparse is not a fetch")
	chs, err := db.ChapterBodies(ctx, 1001)
	require.NoError(t, err)
	require.Len(t, chs, 3)
	require.Contains(t, chs[0].SummaryHTML, "chapter summary")
	m, err := filepath.Glob(filepath.Join(dir, "debug", "work-9-*.skel.html"))
	require.NoError(t, err)
	require.Len(t, m, 1, "expected a debug skeleton for the broken page")

	// Newer listing blurb wins over an older page on --all.
	upsert(model.Work{ID: 1001, Title: "Newer Blurb Title", Stats: model.Stats{Kudos: 5000}}, model.FromBlurb, parse.Version)
	res, err = ctl.Reparse(ctx, ReparseOptions{All: true})
	require.NoError(t, err)
	require.Equal(t, 0, res.ImagesQueued, "already-queued images must not be counted again")
	w := work(1001)
	require.Equal(t, "Newer Blurb Title", w.Title, "reparse must not roll back newer listing data")
	require.Equal(t, 5000, w.Stats.Kudos)
	require.NotContains(t, strings.ToLower(logs.String()), "fixture title", "reparse logs leaked content")
}

func TestOfflineControllersNeverFetch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctl := New(Options{Store: st, Sources: config.Default().Sources})
	_, err = ctl.FetchWork(ctx, 1, 0)
	require.ErrorIs(t, err, ErrOffline)
	_, err = ctl.AddSource(ctx, "/tags/Fixture%20Fandom/works", AddSourceOptions{})
	require.ErrorIs(t, err, ErrOffline)
}
