package parse

import (
	"bytes"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Series is a parsed /series/<id> page (metadata + one page of work blurbs).
type Series struct {
	ID          int64
	Title       string
	Creators    []model.Creator
	Begun       string
	Updated     string
	SummaryHTML string
	NotesHTML   string
	Words       int
	WorksCount  int
	Complete    bool
	Bookmarks   int
	Listing     Listing
}

// ParseSeries parses a series page per app/views/series/show.html.erb.
func ParseSeries(seriesID int64, page []byte, fetchedAt time.Time) (Series, *Report, error) {
	start := time.Now()
	r := newReport("series")
	defer func() { r.Duration = time.Since(start) }()
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return Series{}, r, err
	}
	s := Series{ID: seriesID}
	main, err := r.need("#main", doc.Find("#main"))
	if err != nil {
		return s, r, err
	}
	meta, err := r.need("dl.series.meta", main.Find("dl.series.meta").First())
	if err != nil {
		return s, r, err
	}
	s.Title = clean(main.ChildrenFiltered("h2.heading").First().Text())
	s.Creators = creators(meta)

	// dt/dd pairs keyed by label text (labels are fixed English UI strings, not content).
	meta.ChildrenFiltered("dt").Each(func(_ int, dt *goquery.Selection) {
		dd := dt.NextFiltered("dd")
		switch label := clean(dt.Text()); {
		case strings.HasPrefix(label, "Series Begun"):
			s.Begun, _ = parseISODate(dd.Text())
		case strings.HasPrefix(label, "Series Updated"):
			s.Updated, _ = parseISODate(dd.Text())
		case strings.HasPrefix(label, "Description"):
			s.SummaryHTML = innerHTML(dd.Find("blockquote.userstuff"))
		case strings.HasPrefix(label, "Notes"):
			s.NotesHTML = innerHTML(dd.Find("blockquote.userstuff"))
		}
	})
	stats := r.sel("dl.series.meta dl.stats", meta.Find("dl.stats").First())
	s.Words = atoi(stats.Find("dd.words").Text())
	s.WorksCount = atoi(stats.Find("dd.works").Text())
	s.Bookmarks = atoi(stats.Find("dd.bookmarks").Text())
	stats.ChildrenFiltered("dt").Each(func(_ int, dt *goquery.Selection) {
		if strings.HasPrefix(clean(dt.Text()), "Complete") {
			s.Complete = clean(dt.NextFiltered("dd").Text()) == "Yes"
		}
	})

	s.Listing.Total = s.WorksCount
	s.Listing.Pagination = parsePagination(main, r)
	s.Listing.Blurbs, s.Listing.Skipped = parseBlurbs(main, fetchedAt, r)

	r.textLen("title", s.Title)
	r.textLen("summary", s.SummaryHTML)
	r.num("works_count", s.WorksCount)
	r.num("words", s.Words)
	r.Fields["has_begun"] = s.Begun != ""
	r.Counts["creators"] = len(s.Creators)
	return s, r, nil
}
