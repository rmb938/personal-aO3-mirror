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

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func fixture(t *testing.T, name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Work 1001: stored raw page, parsed by an "old" parser (version 0), metadata stale.
	if _, err := st.PutRawPage(ctx, 1001, "/works/1001?view_full_work=true", fixture(t, "work_chaptered.html"), 1); err != nil {
		t.Fatal(err)
	}
	st.UpsertWork(ctx, model.Work{ID: 1001, Title: "stale"}, model.FromPage, 0)
	st.SetWorkStatus(ctx, 1001, model.StatusDeleted)
	// Work 1002: already current; Work 9: broken raw page.
	st.PutRawPage(ctx, 1002, "/works/1002?view_full_work=true", fixture(t, "work_oneshot.html"), 1)
	st.UpsertWork(ctx, model.Work{ID: 1002, Title: "current"}, model.FromPage, parse.Version)
	st.PutRawPage(ctx, 9, "/works/9", []byte(`<html><body><div id="outer"><div id="main">x</div></div></body></html>`), 1)
	st.UpsertWork(ctx, model.Work{ID: 9}, model.FromPage, 0)

	var lastFetchedBefore int64
	st.Meta.QueryRow(`SELECT last_fetched FROM works WHERE id = 1001`).Scan(&lastFetchedBefore)
	clk = clk.Add(time.Hour)

	res, err := Run(ctx, st, Options{DataDir: dir, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	if res.Seen != 3 || res.Reparsed != 1 || res.Skipped != 1 || res.Failed != 1 {
		t.Fatalf("result %+v", res)
	}
	var title, status string
	var pv int
	var lastFetched int64
	st.Meta.QueryRow(`SELECT title, status, parser_version, last_fetched FROM works WHERE id = 1001`).Scan(&title, &status, &pv, &lastFetched)
	if title != "Fixture Title One" || status != "deleted" || pv != parse.Version || lastFetched != lastFetchedBefore {
		t.Fatalf("1001 after reparse: title=%q status=%s pv=%d last_fetched %d->%d", title, status, pv, lastFetchedBefore, lastFetched)
	}
	if chs, _ := st.ChapterBodies(ctx, 1001); len(chs) != 3 || !strings.Contains(chs[0].SummaryHTML, "chapter summary") {
		t.Fatalf("bodies: %d", len(chs))
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "debug", "work-9-*.skel.html")); len(m) != 1 {
		t.Fatalf("expected debug skeleton for broken page, got %v", m)
	}

	// Newer listing blurb wins over an older page on --all.
	st.UpsertWork(ctx, model.Work{ID: 1001, Title: "Newer Blurb Title", Stats: model.Stats{Kudos: 5000}}, model.FromBlurb, parse.Version)
	if _, err := Run(ctx, st, Options{All: true, Logger: log}); err != nil {
		t.Fatal(err)
	}
	var kudos int
	st.Meta.QueryRow(`SELECT title, kudos FROM works WHERE id = 1001`).Scan(&title, &kudos)
	if title != "Newer Blurb Title" || kudos != 5000 {
		t.Fatalf("reparse rolled back newer listing data: %q %d", title, kudos)
	}
	if strings.Contains(strings.ToLower(logs.String()), "fixture title") {
		t.Fatal("reparse logs leaked content")
	}
}
