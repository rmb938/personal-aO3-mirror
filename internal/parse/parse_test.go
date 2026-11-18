package parse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
)

var fetchedAt = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	require.NoError(t, err)
	return b
}

func tagsOf(w model.Work, typ model.TagType) []string {
	var out []string
	for _, t := range w.Tags {
		if t.Type == typ {
			out = append(out, t.Name)
		}
	}
	return out
}

func TestParseListing(t *testing.T) {
	l, r, err := ParseListing(fixture(t, "listing_tag.html"), fetchedAt)
	require.NoError(t, err, "missing: %v", r.Missing)
	require.Equal(t, 1234, l.Total)
	require.Equal(t, 2, l.Pagination.Current)
	require.Equal(t, 62, l.Pagination.Last)
	require.Len(t, l.Blurbs, 2)
	require.Equal(t, 1, l.Skipped)

	a := l.Blurbs[0]
	a2 := a
	a2.Tags = nil
	require.Equal(t, model.Work{
		ID: 1001, Title: "Fixture Title One", Restricted: true,
		Creators: []model.Creator{{User: "fixture_author", Pseud: "fixture_pseud"}, {User: "second_author", Pseud: "second_author"}},
		Language: "English", Words: 12345, ChaptersPosted: 3, ChaptersExpected: 0, Revised: "2026-11-01",
		Stats:       model.Stats{Kudos: 1200, Hits: 54321, Comments: 45, Bookmarks: 300},
		Series:      []model.SeriesPart{{ID: 3001, Title: "Fixture Series", Position: 2}},
		SummaryHTML: "<p>Lorem ipsum fixture summary.</p>",
	}, a2, "blurb 1")
	for typ, names := range map[model.TagType][]string{
		model.TagRating:    {"Teen And Up Audiences"},
		model.TagWarning:   {"No Archive Warnings Apply"},
		model.TagCategory:  {"F/M", "Gen"},
		model.TagFandom:    {"Fixture Fandom", "Other Fixture Fandom & Co."},
		model.TagRelation:  {"Fixture A/Fixture B"},
		model.TagCharacter: {"Fixture A", "Fixture B"},
		model.TagFreeform:  {"fixture freeform"},
	} {
		assert.Equal(t, names, tagsOf(a, typ), "%s tags", typ)
	}

	b := l.Blurbs[1]
	require.True(t, b.Anonymous)
	require.Empty(t, b.Creators)
	require.True(t, b.Complete)
	require.True(t, b.RevisedApprox)
	require.Equal(t, "2026-11-12", b.Revised)
	require.Empty(t, tagsOf(b, model.TagCategory), "'No category' must not become a tag")
}

// syntheticListing builds a hand-written listing page of n fake work blurbs, optionally
// without their fandom heading or stats block.
func syntheticListing(n int, fandoms, stats bool) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `<html><body><div id="outer"><div id="main"><h2 class="heading">%d Works in Fixture Tag</h2><ol class="work index group">`, n)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<li id="work_%d" class="work blurb group" role="article"><div class="header module">`+
			`<h4 class="heading"><a href="/works/%d">Fixture Work %d</a></h4>`, i, i, i)
		if fandoms {
			b.WriteString(`<h5 class="fandoms heading"><a class="tag" href="/tags/Fixture%20Tag/works">Fixture Tag</a></h5>`)
		}
		b.WriteString(`<p class="datetime">01 Nov 2026</p></div>` +
			`<ul class="tags commas"><li class="warnings"><strong><a class="tag" href="/tags/x/works">Fixture Warning</a></strong></li></ul>`)
		if stats {
			b.WriteString(`<dl class="stats"><dt class="words">Words:</dt><dd class="words">100</dd>` +
				`<dt class="chapters">Chapters:</dt><dd class="chapters">1/1</dd></dl>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ol></div></div></body></html>`)
	return []byte(b.String())
}

// A page of work blurbs none of which has a fandom link, or none a stats block, fails as
// unexpected (the markup changed); one odd blurb, or a short page, does not.
func TestParseListingMissingBlurbSections(t *testing.T) {
	_, r, err := ParseListing(syntheticListing(5, true, true), fetchedAt)
	require.NoError(t, err, "missing: %v", r.Missing)
	require.Equal(t, 5, r.Counts["blurbs_with_fandom"])
	require.Equal(t, 5, r.Counts["blurbs_with_stats"])

	_, r, err = ParseListing(syntheticListing(5, false, true), fetchedAt)
	require.ErrorIs(t, err, ErrUnexpected)
	require.Equal(t, []string{"li.blurb h5.fandoms a.tag"}, r.Missing)

	_, r, err = ParseListing(syntheticListing(5, true, false), fetchedAt)
	require.ErrorIs(t, err, ErrUnexpected)
	require.Equal(t, []string{"li.blurb dl.stats"}, r.Missing)

	l, r, err := ParseListing(syntheticListing(4, false, false), fetchedAt)
	require.NoError(t, err, "a short page is not checked: %v", r.Missing)
	require.Len(t, l.Blurbs, 4)

	// One blurb without a fandom among normal ones is fine.
	odd := strings.Replace(string(syntheticListing(5, true, true)),
		`<h5 class="fandoms heading"><a class="tag" href="/tags/Fixture%20Tag/works">Fixture Tag</a></h5>`, "", 1)
	_, r, err = ParseListing([]byte(odd), fetchedAt)
	require.NoError(t, err, "missing: %v", r.Missing)
	require.Equal(t, 4, r.Counts["blurbs_with_fandom"])
}

func TestParseWorkChaptered(t *testing.T) {
	w, r, err := ParseWork(1001, fixture(t, "work_chaptered.html"))
	require.NoError(t, err, "missing: %v", r.Missing)
	require.Equal(t, "Fixture Title One", w.Title)
	require.True(t, w.Restricted)
	require.False(t, w.Anonymous)
	require.Len(t, w.Creators, 2)
	require.Equal(t, "2026-10-01", w.Published)
	require.Equal(t, "2026-11-01", w.Revised)
	require.Equal(t, 12345, w.Words)
	require.Equal(t, 3, w.ChaptersPosted)
	require.Equal(t, 0, w.ChaptersExpected)
	require.False(t, w.Complete)
	require.Equal(t, model.Stats{Kudos: 1200, Hits: 54321, Comments: 45, Bookmarks: 300}, w.Stats)
	require.Equal(t, []string{"Fixture Fandom", "Other Fixture Fandom & Co."}, tagsOf(w, model.TagFandom))
	require.Equal(t, []string{"F/M", "Gen"}, tagsOf(w, model.TagCategory))
	require.Len(t, w.Tags, 10)
	require.Equal(t, []model.SeriesPart{{ID: 3001, Title: "Fixture Series", Position: 2}}, w.Series)
	require.Equal(t, []model.Collection{{Name: "fixture_collection", Title: "Fixture Collection"}}, w.Collections)
	require.Contains(t, w.SummaryHTML, "fixture summary")
	require.Contains(t, w.NotesHTML, "Fixture work notes")
	require.Contains(t, w.NotesHTML, "associations")
	require.NotContains(t, w.NotesHTML, "Notes:", "notes heading must be stripped")
	require.Contains(t, w.EndNotesHTML, "Fixture work end notes")
	require.Contains(t, w.SkinCSS, ".fixture-class")

	require.Len(t, w.Chapters, 3)
	c1, c2, c3 := w.Chapters[0], w.Chapters[1], w.Chapters[2]
	require.Equal(t, int64(2001), c1.ID)
	require.Equal(t, 1, c1.Position)
	require.Equal(t, "Fixture Chapter Alpha", c1.Title)
	require.Contains(t, c1.SummaryHTML, "chapter summary")
	require.Contains(t, c1.NotesHTML, "chapter notes")
	require.Contains(t, c1.EndNotesHTML, "chapter end notes")
	require.Contains(t, c1.BodyHTML, "body one")
	require.NotContains(t, c1.BodyHTML, "Chapter Text", "landmark heading must be stripped")
	require.Equal(t, int64(2002), c2.ID)
	require.Empty(t, c2.Title)
	require.Equal(t, "Fixture: With Colon", c3.Title)
	require.Equal(t, 1, r.Counts["images"])
	require.Equal(t, 3, r.Counts["chapters"])
}

func TestParseWorkOneshot(t *testing.T) {
	w, r, err := ParseWork(1002, fixture(t, "work_oneshot.html"))
	require.NoError(t, err, "missing: %v", r.Missing)
	require.True(t, w.Anonymous)
	require.Equal(t, "2026-11-12", w.Revised)
	require.Equal(t, "2026-11-12", w.Published)
	require.True(t, w.Complete)
	require.Len(t, w.Chapters, 1)
	require.Contains(t, w.Chapters[0].BodyHTML, "oneshot body")
	require.Empty(t, w.SkinCSS)
}

func TestParseWorkUnexpected(t *testing.T) {
	_, r, err := ParseWork(1, []byte(`<html><body><div id="outer"><div id="main"><p>x</p></div></div></body></html>`))
	require.ErrorIs(t, err, ErrUnexpected)
	require.NotEmpty(t, r.Missing)
	require.NotContains(t, err.Error(), "<p>", "error must name selectors only")
}

// A chapter whose text container is missing fails the parse, so an empty chapter never
// replaces a stored good one.
func TestParseWorkChapterMissingBody(t *testing.T) {
	page := string(fixture(t, "work_chaptered.html"))
	broken := strings.Replace(page, `<div class="userstuff module" role="article">`, `<div class="fixture-renamed module" role="article">`, 1)
	require.NotEqual(t, page, broken, "fixture must contain a chapter body to break")
	_, r, err := ParseWork(1001, []byte(broken))
	require.ErrorIs(t, err, ErrUnexpected)
	require.Contains(t, r.Missing, "#chapters>div.chapter>div.userstuff")
	require.Equal(t, 1, r.Counts["chapter_missing_body"])
}

func TestParseSeries(t *testing.T) {
	s, r, err := ParseSeries(3001, fixture(t, "series.html"), fetchedAt)
	require.NoError(t, err, "missing: %v", r.Missing)
	require.Equal(t, "Fixture Series", s.Title)
	require.Equal(t, "2026-01-05", s.Begun)
	require.Equal(t, "2026-11-01", s.Updated)
	require.Equal(t, 13145, s.Words)
	require.Equal(t, 2, s.WorksCount)
	require.False(t, s.Complete)
	require.Equal(t, 7, s.Bookmarks)
	require.Len(t, s.Creators, 1)
	require.Contains(t, s.SummaryHTML, "series description")
	require.Contains(t, s.NotesHTML, "series notes")
	require.Len(t, s.Listing.Blurbs, 2)
	require.Equal(t, int64(999), s.Listing.Blurbs[0].ID)
	require.Equal(t, 2, s.Listing.Blurbs[1].Series[0].Position)
}

func TestReportIsContentFree(t *testing.T) {
	for _, f := range []string{"work_chaptered.html", "work_oneshot.html", "listing_tag.html", "series.html"} {
		var r *Report
		var err error
		switch {
		case strings.HasPrefix(f, "work"):
			_, r, err = ParseWork(1, fixture(t, f))
		case strings.HasPrefix(f, "series"):
			_, r, err = ParseSeries(1, fixture(t, f), fetchedAt)
		default:
			_, r, err = ParseListing(fixture(t, f), fetchedAt)
		}
		require.NoError(t, err, f)
		var buf bytes.Buffer
		for _, a := range r.Attrs() {
			buf.WriteString(strings.ToLower(fmt.Sprint(a)) + " ")
		}
		for _, word := range []string{"lorem", "fixture title", "fixture_author", "fixture fandom", "fixture series"} {
			assert.NotContains(t, buf.String(), word, "%s: report leaked content", f)
		}
	}
}

func TestBlurbDates(t *testing.T) {
	for in, want := range map[string]struct {
		date   string
		approx bool
	}{
		"01 Nov 2026":            {"2026-11-01", false},
		"less than 1 minute ago": {"2026-11-15", true},
		"1 minute ago":           {"2026-11-15", true},
		"5 hours ago":            {"2026-11-15", true},
		"1 day ago":              {"2026-11-14", true},
		"3 days ago":             {"2026-11-12", true},
		"30 days ago":            {"2026-10-16", true},
	} {
		d, approx, ok := parseBlurbDate(in, fetchedAt)
		if assert.True(t, ok, "%q should parse", in) {
			assert.Equal(t, want.date, d, "%q", in)
			assert.Equal(t, want.approx, approx, "%q approx", in)
		}
	}
}

// Skeletons: fixtures must survive skeletonization with structure intact, and skeletons must
// contain no text. Real skeletons supplied by the user (testdata/skeletons) are checked too.
func TestSkeletonsKeepStructure(t *testing.T) {
	for _, f := range []string{"work_chaptered.html", "work_oneshot.html", "listing_tag.html", "series.html", "tag_character.html"} {
		t.Run(f, func(t *testing.T) {
			skel, err := skeleton.Skeletonize(bytes.NewReader(fixture(t, f)))
			require.NoError(t, err)
			low := strings.ToLower(string(skel))
			for _, word := range []string{"fixture", "english"} {
				assert.NotContains(t, low, word, "skeleton leaked content")
			}
			checkSkeleton(t, f, skel)
		})
	}
	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "skeletons", "*.skel.html"))
	require.NoError(t, err)
	for _, m := range matches {
		t.Run(filepath.Base(m), func(t *testing.T) {
			b, err := os.ReadFile(m)
			require.NoError(t, err)
			checkSkeleton(t, filepath.Base(m), b)
		})
	}
}

// checkSkeleton parses a skeleton and asserts the parser found real structure and values.
// Skeleton values are random but keep their shape, so non-zero numbers/dates/ids are expected.
func checkSkeleton(t *testing.T, name string, skel []byte) {
	t.Helper()
	switch {
	case strings.HasPrefix(name, "work"):
		w, r, err := ParseWork(1, skel)
		require.NoError(t, err, "%s: work skeleton failed, missing=%v", name, r.Missing)
		var problems []string
		if len(w.Chapters) == 0 {
			problems = append(problems, "no chapters")
		}
		if len(tagsOf(w, model.TagRating)) == 0 || len(tagsOf(w, model.TagFandom)) == 0 {
			problems = append(problems, "no rating/fandom tags")
		}
		if w.Words == 0 || w.ChaptersPosted == 0 || w.Published == "" || w.Revised == "" {
			problems = append(problems, fmt.Sprintf("stats words=%d chapters=%d published=%t revised=%t", w.Words, w.ChaptersPosted, w.Published != "", w.Revised != ""))
		}
		if w.Title == "" || (len(w.Creators) == 0 && !w.Anonymous) {
			problems = append(problems, "no title/creators")
		}
		if len(w.Chapters) > 1 {
			for _, c := range w.Chapters {
				if c.ID == 0 || c.BodyHTML == "" {
					problems = append(problems, fmt.Sprintf("chapter %d id=%d body=%d", c.Position, c.ID, len(c.BodyHTML)))
				}
			}
		}
		if len(w.Chapters) != w.ChaptersPosted {
			problems = append(problems, fmt.Sprintf("chapters parsed %d != posted %d", len(w.Chapters), w.ChaptersPosted))
		}
		assert.Empty(t, problems, "%s\nreport: %v", name, r.Attrs())
	case strings.HasPrefix(name, "listing"):
		l, r, err := ParseListing(skel, fetchedAt)
		require.NoError(t, err, "%s: listing skeleton failed", name)
		checkBlurbs(t, name, l, r)
		assert.Positive(t, l.Total, "%s: no total parsed from heading", name)
	case strings.HasPrefix(name, "tag"):
		ti, r, err := ParseTag(skel)
		require.NoError(t, err, "%s: tag skeleton failed, missing=%v", name, r.Missing)
		assert.NotEmpty(t, ti.Name, "%s: tag name, report=%v", name, r.Attrs())
		assert.NotEqual(t, model.TagUnknown, ti.Type, "%s: tag type, report=%v", name, r.Attrs())
	case strings.HasPrefix(name, "series"):
		s, r, err := ParseSeries(1, skel, fetchedAt)
		require.NoError(t, err, "%s: series skeleton failed", name)
		assert.True(t, s.Title != "" && s.Begun != "" && s.Updated != "" && s.WorksCount != 0 && s.Words != 0 && len(s.Creators) != 0,
			"%s: series meta incomplete: title=%t begun=%t updated=%t works=%d words=%d creators=%d\nreport: %v",
			name, s.Title != "", s.Begun != "", s.Updated != "", s.WorksCount, s.Words, len(s.Creators), r.Attrs())
		checkBlurbs(t, name, s.Listing, r)
	}
}

func checkBlurbs(t *testing.T, name string, l Listing, r *Report) {
	t.Helper()
	if !assert.NotEmpty(t, l.Blurbs, "%s: no blurbs parsed (skipped=%d)\nreport: %v", name, l.Skipped, r.Attrs()) {
		return
	}
	for i, b := range l.Blurbs {
		var p []string
		if b.ID == 0 || b.Title == "" {
			p = append(p, "id/title")
		}
		if len(tagsOf(b, model.TagFandom)) == 0 || len(tagsOf(b, model.TagRating)) == 0 {
			p = append(p, "fandom/rating tags")
		}
		if b.Words == 0 || b.ChaptersPosted == 0 || b.Revised == "" {
			p = append(p, fmt.Sprintf("stats words=%d chapters=%d revised=%t", b.Words, b.ChaptersPosted, b.Revised != ""))
		}
		if len(b.Creators) == 0 && !b.Anonymous {
			p = append(p, "creators")
		}
		assert.Empty(t, p, "%s: blurb %d", name, i)
	}
}
