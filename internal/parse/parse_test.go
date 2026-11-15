package parse

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
)

var fetchedAt = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err, r.Missing)
	}
	if l.Total != 1234 || l.Pagination.Current != 2 || l.Pagination.Last != 62 || !l.Pagination.HasNext {
		t.Fatalf("total/pagination: %d %+v", l.Total, l.Pagination)
	}
	if len(l.Blurbs) != 2 || l.Skipped != 1 {
		t.Fatalf("blurbs=%d skipped=%d", len(l.Blurbs), l.Skipped)
	}
	a := l.Blurbs[0]
	want := model.Work{
		ID: 1001, Title: "Fixture Title One", Restricted: true,
		Creators: []model.Creator{{User: "fixture_author", Pseud: "fixture_pseud"}, {User: "second_author", Pseud: "second_author"}},
		Language: "English", Words: 12345, ChaptersPosted: 3, ChaptersExpected: 0, Revised: "2026-11-01",
		Stats:       model.Stats{Kudos: 1200, Hits: 54321, Comments: 45, Bookmarks: 300},
		Series:      []model.SeriesPart{{ID: 3001, Title: "Fixture Series", Position: 2}},
		SummaryHTML: "<p>Lorem ipsum fixture summary.</p>",
	}
	a2 := a
	a2.Tags = nil
	if !reflect.DeepEqual(a2, want) {
		t.Fatalf("blurb 1:\n got %+v\nwant %+v", a2, want)
	}
	for typ, names := range map[model.TagType][]string{
		model.TagRating:    {"Teen And Up Audiences"},
		model.TagWarning:   {"No Archive Warnings Apply"},
		model.TagCategory:  {"F/M", "Gen"},
		model.TagFandom:    {"Fixture Fandom", "Other Fixture Fandom & Co."},
		model.TagRelation:  {"Fixture A/Fixture B"},
		model.TagCharacter: {"Fixture A", "Fixture B"},
		model.TagFreeform:  {"fixture freeform"},
	} {
		if got := tagsOf(a, typ); !reflect.DeepEqual(got, names) {
			t.Errorf("%s tags = %v, want %v", typ, got, names)
		}
	}

	b := l.Blurbs[1]
	if !b.Anonymous || len(b.Creators) != 0 || !b.Complete || !b.RevisedApprox || b.Revised != "2026-11-12" {
		t.Fatalf("blurb 2: %+v", b)
	}
	if got := tagsOf(b, model.TagCategory); len(got) != 0 {
		t.Fatalf("'No category' must not become a tag: %v", got)
	}
}

func TestParseWorkChaptered(t *testing.T) {
	w, r, err := ParseWork(1001, fixture(t, "work_chaptered.html"))
	if err != nil {
		t.Fatal(err, r.Missing)
	}
	if w.Title != "Fixture Title One" || !w.Restricted || w.Anonymous || len(w.Creators) != 2 {
		t.Fatalf("header: %+v", w)
	}
	if w.Published != "2026-10-01" || w.Revised != "2026-11-01" || w.Words != 12345 ||
		w.ChaptersPosted != 3 || w.ChaptersExpected != 0 || w.Complete {
		t.Fatalf("stats: pub=%s rev=%s words=%d ch=%d/%d complete=%v", w.Published, w.Revised, w.Words, w.ChaptersPosted, w.ChaptersExpected, w.Complete)
	}
	if w.Stats != (model.Stats{Kudos: 1200, Hits: 54321, Comments: 45, Bookmarks: 300}) {
		t.Fatalf("stats: %+v", w.Stats)
	}
	if !reflect.DeepEqual(tagsOf(w, model.TagFandom), []string{"Fixture Fandom", "Other Fixture Fandom & Co."}) ||
		!reflect.DeepEqual(tagsOf(w, model.TagCategory), []string{"F/M", "Gen"}) || len(w.Tags) != 10 {
		t.Fatalf("tags: %+v", w.Tags)
	}
	if !reflect.DeepEqual(w.Series, []model.SeriesPart{{ID: 3001, Title: "Fixture Series", Position: 2}}) {
		t.Fatalf("series: %+v", w.Series)
	}
	if !reflect.DeepEqual(w.Collections, []model.Collection{{Name: "fixture_collection", Title: "Fixture Collection"}}) {
		t.Fatalf("collections: %+v", w.Collections)
	}
	if !strings.Contains(w.SummaryHTML, "fixture summary") || !strings.Contains(w.NotesHTML, "Fixture work notes") ||
		!strings.Contains(w.NotesHTML, "associations") || strings.Contains(w.NotesHTML, "Notes:") {
		t.Fatalf("notes/summary: %q / %q", w.SummaryHTML, w.NotesHTML)
	}
	if !strings.Contains(w.EndNotesHTML, "Fixture work end notes") || !strings.Contains(w.SkinCSS, ".fixture-class") {
		t.Fatalf("endnotes/skin: %q / %q", w.EndNotesHTML, w.SkinCSS)
	}
	if len(w.Chapters) != 3 {
		t.Fatalf("chapters = %d", len(w.Chapters))
	}
	c1, c2, c3 := w.Chapters[0], w.Chapters[1], w.Chapters[2]
	if c1.ID != 2001 || c1.Position != 1 || c1.Title != "Fixture Chapter Alpha" ||
		!strings.Contains(c1.SummaryHTML, "chapter summary") || !strings.Contains(c1.NotesHTML, "chapter notes") ||
		!strings.Contains(c1.EndNotesHTML, "chapter end notes") || !strings.Contains(c1.BodyHTML, "body one") ||
		strings.Contains(c1.BodyHTML, "Chapter Text") {
		t.Fatalf("chapter 1: %+v", c1)
	}
	if c2.ID != 2002 || c2.Title != "" || c3.Title != "Fixture: With Colon" {
		t.Fatalf("chapter titles: %q %q", c2.Title, c3.Title)
	}
	if r.Counts["images"] != 1 || r.Counts["chapters"] != 3 {
		t.Fatalf("report counts: %+v", r.Counts)
	}
}

func TestParseWorkOneshot(t *testing.T) {
	w, r, err := ParseWork(1002, fixture(t, "work_oneshot.html"))
	if err != nil {
		t.Fatal(err, r.Missing)
	}
	if !w.Anonymous || w.Revised != "2026-11-12" || w.Published != "2026-11-12" || !w.Complete || len(w.Chapters) != 1 ||
		!strings.Contains(w.Chapters[0].BodyHTML, "oneshot body") || w.SkinCSS != "" {
		t.Fatalf("oneshot: %+v", w)
	}
}

func TestParseWorkUnexpected(t *testing.T) {
	_, r, err := ParseWork(1, []byte(`<html><body><div id="outer"><div id="main"><p>x</p></div></div></body></html>`))
	if !errors.Is(err, ErrUnexpected) || len(r.Missing) == 0 {
		t.Fatalf("want ErrUnexpected, got %v (%v)", err, r.Missing)
	}
	if strings.Contains(err.Error(), "<p>") {
		t.Fatal("error must name selectors only")
	}
}

func TestParseSeries(t *testing.T) {
	s, r, err := ParseSeries(3001, fixture(t, "series.html"), fetchedAt)
	if err != nil {
		t.Fatal(err, r.Missing)
	}
	if s.Title != "Fixture Series" || s.Begun != "2026-01-05" || s.Updated != "2026-11-01" || s.Words != 13145 ||
		s.WorksCount != 2 || s.Complete || s.Bookmarks != 7 || len(s.Creators) != 1 ||
		!strings.Contains(s.SummaryHTML, "series description") || !strings.Contains(s.NotesHTML, "series notes") {
		t.Fatalf("series: %+v", s)
	}
	if len(s.Listing.Blurbs) != 2 || s.Listing.Blurbs[0].ID != 999 || s.Listing.Blurbs[1].Series[0].Position != 2 {
		t.Fatalf("series blurbs: %+v", s.Listing.Blurbs)
	}
}

func TestReportIsContentFree(t *testing.T) {
	for _, f := range []string{"work_chaptered.html", "work_oneshot.html", "listing_tag.html", "series.html"} {
		var r *Report
		switch {
		case strings.HasPrefix(f, "work"):
			_, r, _ = ParseWork(1, fixture(t, f))
		case strings.HasPrefix(f, "series"):
			_, r, _ = ParseSeries(1, fixture(t, f), fetchedAt)
		default:
			_, r, _ = ParseListing(fixture(t, f), fetchedAt)
		}
		var buf bytes.Buffer
		for _, a := range r.Attrs() {
			buf.WriteString(strings.ToLower(fmt.Sprint(a)) + " ")
		}
		for _, word := range []string{"lorem", "fixture title", "fixture_author", "fixture fandom", "fixture series"} {
			if strings.Contains(buf.String(), word) {
				t.Errorf("%s: report leaked %q: %s", f, word, buf.String())
			}
		}
	}
}

func TestBlurbDates(t *testing.T) {
	for in, want := range map[string]struct {
		date   string
		approx bool
	}{
		"01 Nov 2026":        {"2026-11-01", false},
		"less than a minute": {"2026-11-15", true},
		"about 5 hours":      {"2026-11-15", true},
		"3 days":             {"2026-11-12", true},
		"about 1 month":      {"2026-10-16", true},
	} {
		d, approx, ok := parseBlurbDate(in, fetchedAt)
		if !ok || d != want.date || approx != want.approx {
			t.Errorf("%q => %s %v %v, want %+v", in, d, approx, ok, want)
		}
	}
}

func TestTagParamRoundTrip(t *testing.T) {
	for _, name := range []string{"A/B", "Fandom & Co.", "What?", "Tag #1", "plain"} {
		if got := tagNameFromHref("/tags/" + TagParam(name) + "/works"); got != name {
			t.Errorf("round trip %q => %q (param %s)", name, got, TagParam(name))
		}
	}
}

// Skeletons: fixtures must survive skeletonization with structure intact, and skeletons must
// contain no text. Real skeletons supplied by the user (testdata/skeletons) are checked too.
func TestSkeletonsKeepStructure(t *testing.T) {
	for _, f := range []string{"work_chaptered.html", "work_oneshot.html", "listing_tag.html", "series.html"} {
		skel, err := skeleton.Skeletonize(bytes.NewReader(fixture(t, f)))
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(string(skel))
		for _, word := range []string{"fixture", "english"} {
			if strings.Contains(low, word) {
				t.Errorf("%s skeleton leaked %q", f, word)
			}
		}
		checkSkeleton(t, f, skel)
	}
	matches, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "skeletons", "*.skel.html"))
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		checkSkeleton(t, filepath.Base(m), b)
	}
}

// checkSkeleton parses a skeleton and asserts the parser found real structure and values.
// Skeleton values are random but keep their shape, so non-zero numbers/dates/ids are expected.
func checkSkeleton(t *testing.T, name string, skel []byte) {
	t.Helper()
	switch {
	case strings.HasPrefix(name, "work"):
		w, r, err := ParseWork(1, skel)
		if err != nil {
			t.Errorf("%s: work skeleton failed: %v missing=%v", name, err, r.Missing)
			return
		}
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
		if len(problems) > 0 {
			t.Errorf("%s: %s\nreport: %v", name, strings.Join(problems, "; "), r.Attrs())
		}
	case strings.HasPrefix(name, "listing"):
		l, r, err := ParseListing(skel, fetchedAt)
		if err != nil {
			t.Errorf("%s: listing skeleton failed: %v", name, err)
			return
		}
		checkBlurbs(t, name, l, r)
		if l.Total <= 0 {
			t.Errorf("%s: no total parsed from heading", name)
		}
	case strings.HasPrefix(name, "series"):
		s, r, err := ParseSeries(1, skel, fetchedAt)
		if err != nil {
			t.Errorf("%s: series skeleton failed: %v", name, err)
			return
		}
		if s.Title == "" || s.Begun == "" || s.Updated == "" || s.WorksCount == 0 || s.Words == 0 || len(s.Creators) == 0 {
			t.Errorf("%s: series meta incomplete: title=%t begun=%t updated=%t works=%d words=%d creators=%d\nreport: %v",
				name, s.Title != "", s.Begun != "", s.Updated != "", s.WorksCount, s.Words, len(s.Creators), r.Attrs())
		}
		checkBlurbs(t, name, s.Listing, r)
	}
}

func checkBlurbs(t *testing.T, name string, l Listing, r *Report) {
	t.Helper()
	if len(l.Blurbs) == 0 {
		t.Errorf("%s: no blurbs parsed (skipped=%d)\nreport: %v", name, l.Skipped, r.Attrs())
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
		if len(p) > 0 {
			t.Errorf("%s: blurb %d: %s", name, i, strings.Join(p, "; "))
		}
	}
}
