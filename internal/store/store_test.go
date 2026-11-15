package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// All values below are synthetic fixtures.

type tclock struct{ t time.Time }

func (c *tclock) now() time.Time { return c.t }

func openTest(t *testing.T) (*Store, *tclock, *bytes.Buffer) {
	t.Helper()
	clk := &tclock{t: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	logs := &bytes.Buffer{}
	s, err := Open(context.Background(), t.TempDir(), Options{
		Logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:    clk.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk, logs
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
	for i := 0; i < 2; i++ {
		s, err := Open(ctx, dir, Options{})
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		for _, db := range []string{"meta", "content"} {
			want := map[string]int{"meta": 1, "content": 2}[db]
			if v, err := s.SchemaVersion(ctx, db); err != nil || v != want {
				t.Fatalf("%s schema = %d, %v", db, v, err)
			}
		}
		s.Close()
	}
}

func TestUpsertBlurbThenPage(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)

	r, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !r.New || !r.NeedsFetch {
		t.Fatalf("first blurb: %+v", r)
	}

	// Same blurb again before any fetch: still needs fetch, not changed.
	r, _ = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	if r.New || r.Changed || !r.NeedsFetch {
		t.Fatalf("repeat blurb: %+v", r)
	}

	// Page fetch.
	clk.t = clk.t.Add(time.Hour)
	pw := w
	pw.Published = "2026-10-01"
	pw.NotesHTML = "<p>fixture notes</p>"
	pw.Chapters = []model.Chapter{{ID: 501, Position: 1, Title: "One"}, {ID: 502, Position: 2, Title: "Two"}}
	pw.Collections = []model.Collection{{Name: "fixture_coll", Title: "Fixture Collection"}}
	if _, err := s.UpsertWork(ctx, pw, model.FromPage, 1); err != nil {
		t.Fatal(err)
	}

	// Blurb with only stats changed: no fetch needed, stats updated.
	w.Stats.Kudos = 99
	r, _ = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	if r.Changed || r.NeedsFetch {
		t.Fatalf("stats-only blurb should not need fetch: %+v", r)
	}
	var kudos, chapters, colls int
	var published string
	s.Meta.QueryRow(`SELECT kudos, published FROM works WHERE id = 1`).Scan(&kudos, &published)
	s.Meta.QueryRow(`SELECT COUNT(*) FROM chapters WHERE work_id = 1`).Scan(&chapters)
	s.Meta.QueryRow(`SELECT COUNT(*) FROM work_collections WHERE work_id = 1`).Scan(&colls)
	if kudos != 99 || published != "2026-10-01" || chapters != 2 || colls != 1 {
		t.Fatalf("blurb clobbered page data: kudos=%d published=%q chapters=%d colls=%d", kudos, published, chapters, colls)
	}

	// Relative blurb date ("3 days" => approximate) with nothing else changed: no fetch, date kept.
	ap := w
	ap.Revised, ap.RevisedApprox = "2026-11-12", true
	r, _ = s.UpsertWork(ctx, ap, model.FromBlurb, 1)
	if r.Changed || r.NeedsFetch {
		t.Fatalf("approx date alone should not trigger fetch: %+v", r)
	}
	var revised string
	s.Meta.QueryRow(`SELECT revised_at FROM works WHERE id = 1`).Scan(&revised)
	if revised != "2026-11-01" {
		t.Fatalf("approx date overwrote exact date: %s", revised)
	}

	// New chapter: changed + needs fetch.
	w.ChaptersPosted, w.Words, w.Revised = 3, 1500, "2026-11-10"
	r, _ = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	if !r.Changed || !r.NeedsFetch {
		t.Fatalf("new chapter should need fetch: %+v", r)
	}
}

func TestTagsDedupeAndTypeUpgrade(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	a := fixtureWork(1)
	b := fixtureWork(2)
	b.Tags = []model.Tag{{Type: model.TagUnknown, Name: "FIXTURE FANDOM"}, {Type: model.TagFreeform, Name: "new fixture tag"}}
	s.UpsertWork(ctx, b, model.FromBlurb, 1)
	s.UpsertWork(ctx, a, model.FromBlurb, 1)
	var n int
	var typ string
	s.Meta.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&n)
	s.Meta.QueryRow(`SELECT type FROM tags WHERE name = 'fixture fandom'`).Scan(&typ)
	if n != 5 || typ != "Fandom" {
		t.Fatalf("tags=%d type=%s", n, typ)
	}
	s.Meta.QueryRow(`SELECT COUNT(*) FROM work_tags wt JOIN tags t ON t.id = wt.tag_id WHERE t.name = 'Fixture Fandom'`).Scan(&n)
	if n != 2 {
		t.Fatalf("both works should share the fandom tag, got %d", n)
	}
}

func TestFTS(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	w2 := fixtureWork(2)
	w2.Title, w2.SummaryHTML = "Other Fixture", "<p>nothing</p>"
	s.UpsertWork(ctx, w2, model.FromBlurb, 1)
	for q, want := range map[string]int{"lorem": 1, "fixture": 2, `tags:"fixture character"`: 2, "creators:fixture_author": 2, "alpha": 1} {
		ids, err := s.SearchWorkIDs(ctx, q, 10)
		if err != nil || len(ids) != want {
			t.Errorf("search %q = %v, %v; want %d", q, ids, err, want)
		}
	}
	// Re-upsert must replace, not duplicate, the FTS row.
	w2.Title = "Renamed"
	s.UpsertWork(ctx, w2, model.FromBlurb, 1)
	if ids, _ := s.SearchWorkIDs(ctx, "renamed", 10); len(ids) != 1 {
		t.Fatalf("renamed: %v", ids)
	}
	var rows int
	s.Meta.QueryRow(`SELECT COUNT(*) FROM works_fts`).Scan(&rows)
	if rows != 2 {
		t.Fatalf("fts rows = %d", rows)
	}
}

func TestStatusKeepsContentAndRevives(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(5)
	w.Chapters = []model.Chapter{{Position: 1, BodyHTML: "<p>fixture body</p>"}}
	s.UpsertWork(ctx, w, model.FromPage, 1)
	s.PutWorkContent(ctx, 5, w.Chapters, "")
	if err := s.SetWorkStatus(ctx, 5, model.StatusDeleted); err != nil {
		t.Fatal(err)
	}
	if st, ok, _ := s.WorkStatus(ctx, 5); !ok || st != model.StatusDeleted {
		t.Fatalf("status = %s", st)
	}
	if chs, _ := s.ChapterBodies(ctx, 5); len(chs) != 1 {
		t.Fatal("content must be kept for deleted works")
	}
	r, _ := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	if !r.Revived || r.PrevStatus != model.StatusDeleted {
		t.Fatalf("revive: %+v", r)
	}
	// Unknown id verification records a row.
	if err := s.SetWorkStatus(ctx, 999, model.StatusRestricted); err != nil {
		t.Fatal(err)
	}
	if st, ok, _ := s.WorkStatus(ctx, 999); !ok || st != model.StatusRestricted {
		t.Fatalf("unknown id status = %s %v", st, ok)
	}
}

func TestRawPagesDedupeAndPrune(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	page := func(n int) []byte { return []byte(strings.Repeat("<p>fixture page</p>", 100) + string(rune('a'+n))) }

	isNew, err := s.PutRawPage(ctx, 1, "/works/1", page(0), 2)
	if err != nil || !isNew {
		t.Fatalf("first put: %v %v", isNew, err)
	}
	clk.t = clk.t.Add(time.Minute)
	if isNew, _ = s.PutRawPage(ctx, 1, "/works/1", page(0), 2); isNew {
		t.Fatal("identical page should be a duplicate")
	}
	for i := 1; i <= 3; i++ {
		clk.t = clk.t.Add(time.Minute)
		s.PutRawPage(ctx, 1, "/works/1", page(i), 2)
	}
	var n int
	s.Content.QueryRow(`SELECT COUNT(*) FROM raw_pages WHERE work_id = 1`).Scan(&n)
	if n != 2 {
		t.Fatalf("keep=2 but %d versions", n)
	}
	got, _, err := s.LatestRawPage(ctx, 1)
	if err != nil || !bytes.Equal(got, page(3)) {
		t.Fatalf("latest page mismatch: %v", err)
	}
	if _, _, err := s.LatestRawPage(ctx, 2); err != ErrNoRawPage {
		t.Fatalf("want ErrNoRawPage, got %v", err)
	}
	ids, _ := s.RawPageWorkIDs(ctx, 0, 10)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestChapterBodiesRoundTrip(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	in := []model.Chapter{
		{ID: 1, Position: 1, BodyHTML: "<p>fixture one</p>", NotesHTML: "<p>n</p>", SummaryHTML: "<p>s</p>"},
		{ID: 2, Position: 2, BodyHTML: "<p>fixture two</p>", EndNotesHTML: "<p>e</p>"},
	}
	if err := s.PutWorkContent(ctx, 9, in, "#workskin p { color: red; }"); err != nil {
		t.Fatal(err)
	}
	out, err := s.ChapterBodies(ctx, 9)
	if err != nil || len(out) != 2 {
		t.Fatal(err, len(out))
	}
	for i := range in {
		if out[i].BodyHTML != in[i].BodyHTML || out[i].NotesHTML != in[i].NotesHTML || out[i].EndNotesHTML != in[i].EndNotesHTML ||
			out[i].SummaryHTML != in[i].SummaryHTML {
			t.Fatalf("chapter %d mismatch", i)
		}
	}
	// Replacing shrinks.
	s.PutWorkContent(ctx, 9, in[:1], "")
	if out, _ = s.ChapterBodies(ctx, 9); len(out) != 1 {
		t.Fatalf("replace failed: %d", len(out))
	}
}

func TestCountsAndRequests(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	s.SetWorkStatus(ctx, 2, model.StatusDeleted)
	s.PutRawPage(ctx, 1, "/works/1", []byte("<p>fixture</p>"), 1)
	s.InsertRequest(ctx, RequestRow{Time: clk.t.Add(-2 * time.Hour), ReqID: "a", Kind: "work", Path: "/works/1", Status: 200, Class: "ok"})
	s.InsertRequest(ctx, RequestRow{Time: clk.t.Add(-time.Minute), ReqID: "b", Kind: "work", Path: "/works/2", Status: 404, Class: "not_found"})
	c, err := s.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.WorksByStatus["ok"] != 1 || c.WorksByStatus["deleted"] != 1 || c.WorksNeverFetch != 1 ||
		c.Requests1h != 1 || c.Requests24h != 2 || c.RawPages != 1 || c.Tags != 4 || c.MetaSchema != 1 {
		t.Fatalf("counts: %+v", c)
	}
	if n, _ := s.PruneRequests(ctx, time.Hour); n != 1 {
		t.Fatalf("pruned %d", n)
	}
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
	s.UpsertWork(ctx, w, model.FromPage, 1)
	s.PutWorkContent(ctx, 3, w.Chapters, sentinel)
	s.PutRawPage(ctx, 3, "/works/3", []byte(sentinel), 1)
	if strings.Contains(strings.ToLower(logs.String()), strings.ToLower(sentinel)) {
		t.Fatalf("sentinel leaked into store logs:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), `"work_id":3`) {
		t.Fatalf("expected debug logs with work_id:\n%s", logs.String())
	}
}
