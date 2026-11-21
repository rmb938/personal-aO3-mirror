package store

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// All values below are synthetic fixtures.

type tclock struct{ t time.Time }

func (c *tclock) now() time.Time { return c.t }

// testStore runs every store method in autocommit mode (each statement its own transaction) and
// exposes the raw databases as meta/content (shadowing the Tx fields) for direct test queries.
type testStore struct {
	*Tx
	st      *Store
	meta    *sql.DB
	content *sql.DB
}

func openTest(tb testing.TB) (*testStore, *tclock, *bytes.Buffer) {
	tb.Helper()
	clk := &tclock{t: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	logs := &bytes.Buffer{}
	s, err := Open(context.Background(), tb.TempDir(), Options{
		Logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:    clk.now,
	})
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, s.Close()) })
	return &testStore{Tx: s.AutoCommitForTestOnly(), st: s, meta: s.meta, content: s.content}, clk, logs
}

func fixtureWork(id int64) model.Work {
	return model.Work{
		ID:          id,
		Title:       "Fixture Title Alpha",
		Creators:    []model.Creator{{User: "fixture_author", Pseud: "fixture_pseud"}},
		SummaryHTML: "<p>Fixture summary with <em>lorem</em> ipsum</p>",
		Language:    "English",
		Tags: []model.Tag{
			{Type: model.TagRating, Name: "Fixture Rating"},
			{Type: model.TagFandom, Name: "Fixture Fandom"},
			{Type: model.TagCharacter, Name: "Fixture Character"},
			{Type: model.TagFreeform, Name: "fixture freeform"},
		},
		Words: 1000, ChaptersPosted: 2, ChaptersExpected: 3, Revised: "2026-11-01",
		Stats:  model.Stats{Kudos: 10, Hits: 100, Comments: 1, Bookmarks: 2},
		Series: []model.SeriesPart{{ID: 77, Title: "Fixture Series", Position: 1}},
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for i := range 2 {
		s, err := Open(ctx, dir, Options{})
		require.NoError(t, err, "open %d", i)
		for db, want := range map[string]int{"meta": 7, "content": 1} {
			v, err := s.schemaVersion(ctx, db)
			require.NoError(t, err)
			require.Equal(t, want, v, "%s schema", db)
		}
		require.NoError(t, s.Close())
	}
}

func TestPageSizes(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for i := range 2 { // new files, then reopened existing files
		s, err := Open(ctx, dir, Options{})
		require.NoError(t, err)
		n, err := s.pageSize(ctx, "content")
		require.NoError(t, err)
		require.Equal(t, ContentPageSize, n, "open %d: content page_size", i)
		n, err = s.pageSize(ctx, "meta")
		require.NoError(t, err)
		require.Equal(t, 4096, n, "open %d: meta page_size (default)", i)
		var mode string
		require.NoError(t, s.content.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
		require.Equal(t, "wal", mode)
		require.NoError(t, s.Close())
	}
}

func TestOpenRefusesUnknownMigrations(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir, Options{})
	require.NoError(t, err)
	// Simulate a store created by an older build that had a migration this build lacks.
	_, err = s.meta.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (99, '0099_tags_images.sql', 0)`)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, err = Open(ctx, dir, Options{})
	require.ErrorContains(t, err, "0099_tags_images.sql", "open should refuse an unknown applied migration")
}

func TestUpsertBlurbThenPage(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)

	r, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, r.New, "first blurb: %+v", r)
	require.True(t, r.NeedsFetch, "first blurb: %+v", r)

	// Same blurb again before any fetch: still needs fetch, not changed.
	r, err = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.False(t, r.New || r.Changed, "repeat blurb: %+v", r)
	require.True(t, r.NeedsFetch, "repeat blurb: %+v", r)

	// Page fetch.
	clk.t = clk.t.Add(time.Hour)
	pw := w
	pw.Published = "2026-10-01"
	pw.NotesHTML = "<p>fixture notes</p>"
	pw.Chapters = []model.Chapter{{ID: 501, Position: 1, Title: "One"}, {ID: 502, Position: 2, Title: "Two"}}
	pw.Collections = []model.Collection{{Name: "fixture_coll", Title: "Fixture Collection"}}
	_, err = s.UpsertWork(ctx, pw, model.FromPage, 1)
	require.NoError(t, err)

	// Blurb with only stats changed: no fetch needed, stats updated.
	w.Stats.Kudos = 99
	r, err = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.False(t, r.Changed || r.NeedsFetch, "stats-only blurb should not need fetch: %+v", r)
	var kudos, chapters, colls int
	var published string
	require.NoError(t, s.meta.QueryRow(`SELECT kudos, published FROM works WHERE id = 1`).Scan(&kudos, &published))
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM chapters WHERE work_id = 1`).Scan(&chapters))
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM work_collections WHERE work_id = 1`).Scan(&colls))
	require.Equal(t, 99, kudos)
	require.Equal(t, "2026-10-01", published, "blurb must not clobber page data")
	require.Equal(t, 2, chapters, "blurb must not clobber page data")
	require.Equal(t, 1, colls, "blurb must not clobber page data")

	// Relative blurb date ("3 days" => approximate) with nothing else changed: no fetch, date kept.
	ap := w
	ap.Revised, ap.RevisedApprox = "2026-11-12", true
	r, err = s.UpsertWork(ctx, ap, model.FromBlurb, 1)
	require.NoError(t, err)
	require.False(t, r.Changed || r.NeedsFetch, "approx date alone should not trigger fetch: %+v", r)
	var revised string
	require.NoError(t, s.meta.QueryRow(`SELECT revised_at FROM works WHERE id = 1`).Scan(&revised))
	require.Equal(t, "2026-11-01", revised, "approx date must not overwrite the exact date")

	// Single-chapter page (no Updated date) keeps the blurb's revised date: no refetch loop.
	one := fixtureWork(3)
	one.Revised = "2026-09-09"
	_, err = s.UpsertWork(ctx, one, model.FromBlurb, 1)
	require.NoError(t, err)
	onePage := one
	onePage.Revised, onePage.Published, onePage.RevisedIsPublished = "2026-01-01", "2026-01-01", true
	_, err = s.UpsertWork(ctx, onePage, model.FromPage, 1)
	require.NoError(t, err)
	r, err = s.UpsertWork(ctx, one, model.FromBlurb, 1)
	require.NoError(t, err)
	require.False(t, r.NeedsFetch || r.Changed, "single-chapter page must not cause refetch: %+v", r)

	// New chapter: changed + needs fetch.
	w.ChaptersPosted, w.Words, w.Revised = 3, 1500, "2026-11-10"
	r, err = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, r.Changed && r.NeedsFetch, "new chapter should need fetch: %+v", r)
}

// Listing blurbs can show the revised date a day off the page's UTC date (AO3 caches blurbs
// without the time zone). That must not refetch on every poll or flip revised_at.
func TestBlurbDateOffByOneDay(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)
	w.Revised = "2026-09-22"
	_, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	pw := w
	pw.Revised = "2026-09-23"
	_, err = s.UpsertWork(ctx, pw, model.FromPage, 1)
	require.NoError(t, err)

	revised := func() string {
		var r string
		require.NoError(t, s.meta.QueryRow(`SELECT revised_at FROM works WHERE id = 1`).Scan(&r))
		return r
	}
	for _, d := range []string{"2026-09-22", "2026-09-24", "2026-09-23"} {
		b := w
		b.Revised = d
		r, err := s.UpsertWork(ctx, b, model.FromBlurb, 1)
		require.NoError(t, err)
		require.False(t, r.Changed || r.NeedsFetch, "blurb date %s, page date 2026-09-23: %+v", d, r)
		require.Equal(t, "2026-09-23", revised(), "blurb date %s must not overwrite the page date", d)
	}

	// Two days off: a real date change.
	b := w
	b.Revised = "2026-09-25"
	r, err := s.UpsertWork(ctx, b, model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, r.Changed && r.NeedsFetch, "two days off must refetch: %+v", r)

	// One day off but a new chapter: refetch.
	b.Revised, b.ChaptersPosted, b.Words = "2026-09-22", w.ChaptersPosted+1, w.Words+100
	r, err = s.UpsertWork(ctx, b, model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, r.NeedsFetch, "new chapter must refetch whatever the date: %+v", r)
}

func TestChangeDetails(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)
	_, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)

	pw := w
	pw.Chapters = []model.Chapter{{ID: 501, Position: 1, Title: "One"}, {ID: 502, Position: 2, Title: "Two"}}
	r, err := s.UpsertWork(ctx, pw, model.FromPage, 1)
	require.NoError(t, err)
	require.Nil(t, r.MetaChanged, "first page fetch of a new work is not diffed")

	// Same page again: compared, nothing changed (empty, not nil).
	r, err = s.UpsertWork(ctx, pw, model.FromPage, 1)
	require.NoError(t, err)
	require.NotNil(t, r.MetaChanged)
	require.Empty(t, r.MetaChanged)

	// Title, one tag added, one chapter retitled, stats changed (stats are not reported).
	pw2 := pw
	pw2.Title = "Fixture Title Beta"
	pw2.Tags = append(append([]model.Tag{}, pw.Tags...), model.Tag{Type: model.TagFreeform, Name: "fixture new tag"})
	pw2.Chapters = []model.Chapter{{ID: 501, Position: 1, Title: "One"}, {ID: 502, Position: 2, Title: "Two (edited)"}}
	pw2.Stats.Kudos = 500
	r, err = s.UpsertWork(ctx, pw2, model.FromPage, 1)
	require.NoError(t, err)
	require.Equal(t, []string{"title", "tags+1-0", "chapters+1-1"}, r.MetaChanged)

	// Reparse is never diffed.
	require.NoError(t, s.ReparseWork(ctx, pw, time.Now(), 1))

	// Blurb with a different revised date: needs fetch, with the dates as the reason.
	b := w
	b.Revised = "2026-11-12"
	r, err = s.UpsertWork(ctx, b, model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, r.NeedsFetch)
	reason := map[string]any{}
	for i := 0; i+1 < len(r.FetchReason); i += 2 {
		reason[r.FetchReason[i].(string)] = r.FetchReason[i+1]
	}
	require.Equal(t, map[string]any{"never_fetched": false, "revised_listed": "2026-11-12", "revised_stored": "2026-11-01",
		"revised_fetched": "2026-11-01", "revised_comparable": true, "chapters_listed": 2, "chapters_fetched": 2,
		"words_listed": 1000, "words_fetched": 1000}, reason)

	// A blurb that needs no fetch has no reason.
	r, err = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.Nil(t, r.FetchReason)
}

func TestTagsDedupeAndTypeUpgrade(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	a := fixtureWork(1)
	b := fixtureWork(2)
	b.Tags = []model.Tag{{Type: model.TagUnknown, Name: "FIXTURE FANDOM"}, {Type: model.TagFreeform, Name: "new fixture tag"}}
	_, err := s.UpsertWork(ctx, b, model.FromBlurb, 1)
	require.NoError(t, err)
	_, err = s.UpsertWork(ctx, a, model.FromBlurb, 1)
	require.NoError(t, err)
	var n int
	var typ string
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&n))
	require.NoError(t, s.meta.QueryRow(`SELECT type FROM tags WHERE name = 'fixture fandom'`).Scan(&typ))
	require.Equal(t, 5, n)
	require.Equal(t, "Fandom", typ)
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM work_tags wt JOIN tags t ON t.id = wt.tag_id
		WHERE t.name = 'Fixture Fandom'`).Scan(&n))
	require.Equal(t, 2, n, "both works should share the fandom tag")
}

func TestFTS(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	require.NoError(t, err)
	w2 := fixtureWork(2)
	w2.Title, w2.SummaryHTML = "Other Fixture", "<p>nothing</p>"
	_, err = s.UpsertWork(ctx, w2, model.FromBlurb, 1)
	require.NoError(t, err)
	search := func(q string) []int64 {
		t.Helper()
		page, err := s.FindWorks(ctx, model.WorkFilter{Query: q, Limit: 10})
		require.NoError(t, err)
		return workIDs(page)
	}
	for q, want := range map[string]int{"lorem": 1, "fixture": 2, `"fixture character"`: 2, "fixture_author": 2,
		"fixture_pseud": 2, "alpha": 1} {
		require.Len(t, search(q), want, "search %q", q)
	}
	// Re-upsert must replace, not duplicate, the FTS row.
	w2.Title = "Renamed"
	_, err = s.UpsertWork(ctx, w2, model.FromBlurb, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, search("renamed"))
	require.Empty(t, search("other"))
	var rows int
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM works_fts`).Scan(&rows))
	require.Equal(t, 2, rows)
}

func TestStatusKeepsContentAndRevives(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(5)
	w.Chapters = []model.Chapter{{Position: 1, BodyHTML: "<p>fixture body</p>"}}
	_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
	require.NoError(t, err)
	_, err = s.PutWorkContent(ctx, 5, w.Chapters, "")
	require.NoError(t, err)
	require.NoError(t, s.SetWorkStatus(ctx, 5, model.StatusDeleted))
	st, ok, err := s.WorkStatus(ctx, 5)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, model.StatusDeleted, st)
	chs, err := s.ChapterBodies(ctx, 5)
	require.NoError(t, err)
	require.Len(t, chs, 1, "content must be kept for deleted works")
	r, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, r.Revived, "revive: %+v", r)
	require.Equal(t, model.StatusDeleted, r.PrevStatus)
	// Unknown id verification records a row.
	require.NoError(t, s.SetWorkStatus(ctx, 999, model.StatusRestricted))
	st, ok, err = s.WorkStatus(ctx, 999)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, model.StatusRestricted, st)
}

// Repeated parse failures keep only the newest failed page and never push out a good one; a
// later good page drops the failed one.
func TestFailedRawPagesNeverEvictGood(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	page := func(n int) []byte { return []byte("<p>fixture page</p>" + strconv.Itoa(n)) }
	count := func() (good, failed int) {
		t.Helper()
		require.NoError(t, s.content.QueryRow(`SELECT COUNT(*) FILTER (WHERE parse_failed = 0), COUNT(*) FILTER (WHERE parse_failed = 1)
			FROM raw_pages WHERE work_id = 1`).Scan(&good, &failed))
		return good, failed
	}
	_, err := s.PutRawPage(ctx, 1, "/works/1", page(0), 1)
	require.NoError(t, err)
	for i := 1; i <= 3; i++ {
		clk.t = clk.t.Add(time.Minute)
		_, err := s.PutFailedRawPage(ctx, 1, "/works/1", page(i), 1)
		require.NoError(t, err)
		good, failed := count()
		require.Equal(t, 1, good, "good page evicted by failure %d", i)
		require.Equal(t, 1, failed, "failed pages pile up")
	}
	latest, _, err := s.LatestRawPage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, page(3), latest, "newest failed page is what reparse retries")

	clk.t = clk.t.Add(time.Minute)
	_, err = s.PutRawPage(ctx, 1, "/works/1", page(4), 1)
	require.NoError(t, err)
	good, failed := count()
	require.Equal(t, 1, good)
	require.Equal(t, 0, failed, "failed page outlived a newer good page")
}

// Request and job lookups for one work never pick up other works whose ids share a prefix.
func TestWorkLookupsAreExact(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	for _, p := range []string{"/works/12?view_full_work=true", "/works/123?view_full_work=true", "/works/12/chapters/5", "/works/120"} {
		require.NoError(t, s.InsertRequest(ctx, RequestRow{ReqID: p, Kind: "work", Path: p, Class: "ok"}))
	}
	rows, err := s.RecentRequests(ctx, false, 0, "/works/12", 10)
	require.NoError(t, err)
	var paths []string
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	require.ElementsMatch(t, []string{"/works/12?view_full_work=true", "/works/12/chapters/5"}, paths)

	enqueue(t, s, NewJob{Kind: "work", Target: 12, Priority: 3, DedupeKey: "work:12"})
	enqueue(t, s, NewJob{Kind: "work", Target: 123, Priority: 3, DedupeKey: "work:123"})
	jobs, err := s.FindJobs(ctx, model.JobFilter{DedupeKey: "work:12", Limit: 10})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, int64(12), jobs[0].Target)
}

func TestRawPagesDedupeAndPrune(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	page := func(n int) []byte { return []byte(strings.Repeat("<p>fixture page</p>", 100) + string(rune('a'+n))) } //nolint:gosec // G115: n is a small page index

	isNew, err := s.PutRawPage(ctx, 1, "/works/1", page(0), 2)
	require.NoError(t, err)
	require.True(t, isNew)
	clk.t = clk.t.Add(time.Minute)
	isNew, err = s.PutRawPage(ctx, 1, "/works/1", page(0), 2)
	require.NoError(t, err)
	require.False(t, isNew, "identical page should be a duplicate")
	for i := 1; i <= 3; i++ {
		clk.t = clk.t.Add(time.Minute)
		_, err := s.PutRawPage(ctx, 1, "/works/1", page(i), 2)
		require.NoError(t, err)
	}
	var n int
	require.NoError(t, s.content.QueryRow(`SELECT COUNT(*) FROM raw_pages WHERE work_id = 1`).Scan(&n))
	require.Equal(t, 2, n, "keep=2")
	got, _, err := s.LatestRawPage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, page(3), got, "latest page")
	_, _, err = s.LatestRawPage(ctx, 2)
	require.ErrorIs(t, err, ErrNoRawPage)
	ids, err := s.RawPageWorkIDs(ctx, 0, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, ids)
}

func TestChapterBodiesRoundTrip(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	in := []model.Chapter{
		{ID: 1, Position: 1, BodyHTML: "<p>fixture one</p>", NotesHTML: "<p>n</p>", SummaryHTML: "<p>s</p>"},
		{ID: 2, Position: 2, BodyHTML: "<p>fixture two</p>", EndNotesHTML: "<p>e</p>"},
	}
	const skin = "#workskin p { color: red; }"
	put := func(chs []model.Chapter, skin string) ContentChange {
		t.Helper()
		ch, err := s.PutWorkContent(ctx, 9, chs, skin)
		require.NoError(t, err)
		return ch
	}
	require.Equal(t, ContentNew, put(in, skin))
	require.Equal(t, ContentUnchanged, put(in, skin), "same content stored again")
	edited := append([]model.Chapter(nil), in...)
	edited[1].EndNotesHTML = "<p>e2</p>"
	require.Equal(t, ContentChanged, put(edited, skin), "edited end notes")
	require.Equal(t, ContentChanged, put(edited, ""), "skin removed")
	put(in, skin)
	out, err := s.ChapterBodies(ctx, 9)
	require.NoError(t, err)
	require.Len(t, out, 2)
	for i := range in {
		require.Equal(t, in[i].BodyHTML, out[i].BodyHTML, "chapter %d body", i)
		require.Equal(t, in[i].NotesHTML, out[i].NotesHTML, "chapter %d notes", i)
		require.Equal(t, in[i].EndNotesHTML, out[i].EndNotesHTML, "chapter %d end notes", i)
		require.Equal(t, in[i].SummaryHTML, out[i].SummaryHTML, "chapter %d summary", i)
	}
	// Replacing shrinks.
	put(in[:1], "")
	out, err = s.ChapterBodies(ctx, 9)
	require.NoError(t, err)
	require.Len(t, out, 1, "replace should shrink")
}

func TestCountsAndRequests(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	require.NoError(t, err)
	require.NoError(t, s.SetWorkStatus(ctx, 2, model.StatusDeleted))
	_, err = s.PutRawPage(ctx, 1, "/works/1", []byte("<p>fixture</p>"), 1)
	require.NoError(t, err)
	require.NoError(t, s.InsertRequest(ctx, RequestRow{Time: clk.t.Add(-2 * time.Hour), ReqID: "a", Kind: "work", Path: "/works/1", Status: 200, Class: "ok"}))
	require.NoError(t, s.InsertRequest(ctx, RequestRow{Time: clk.t.Add(-time.Minute), ReqID: "b", Kind: "work", Path: "/works/2", Status: 404, Class: "not_found"}))
	addSource(t, s, "Fixture Tag A", model.SourceActive, 0)
	addSource(t, s, "Fixture Tag B", model.SourceActive, 0)
	addSource(t, s, "Fixture Tag C", model.SourcePaused, 0)
	c, err := s.Counts(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, c.WorksByStatus["ok"])
	require.Equal(t, 1, c.WorksByStatus["deleted"])
	require.Equal(t, 1, c.WorksNeverFetch)
	require.Equal(t, 1, c.Requests1h)
	require.Equal(t, 2, c.Requests24h)
	require.Equal(t, 1, c.RawPages)
	require.Equal(t, 4, c.Tags)
	require.Equal(t, map[string]int{"active": 2, "paused": 1}, c.SourcesByState)
	n, err := s.PruneRequests(ctx, time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "pruned")
}

func TestLogsAreContentFree(t *testing.T) {
	s, _, logs := openTest(t)
	ctx := context.Background()
	const sentinel = "SENTINELcontent"
	w := fixtureWork(3)
	w.Title, w.SummaryHTML = sentinel, sentinel
	w.Tags = append(w.Tags, model.Tag{Type: model.TagFreeform, Name: sentinel})
	w.Creators = []model.Creator{{User: sentinel, Pseud: sentinel}}
	w.Chapters = []model.Chapter{{Position: 1, Title: sentinel, BodyHTML: sentinel}}
	_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
	require.NoError(t, err)
	_, err = s.PutWorkContent(ctx, 3, w.Chapters, sentinel)
	require.NoError(t, err)
	_, err = s.PutRawPage(ctx, 3, "/works/3", []byte(sentinel), 1)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(logs.String()), strings.ToLower(sentinel), "sentinel leaked into store logs")
	require.Contains(t, logs.String(), `"work_id":3`, "expected debug logs with work_id")
}
