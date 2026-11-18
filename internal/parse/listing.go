package parse

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Listing is one page of a works index (/tags/X/works, /users/X/works, /collections/X/works,
// /users/X/bookmarks, series pages).
type Listing struct {
	Total      int // total works per the heading; -1 if not found
	Pagination Pagination
	Blurbs     []model.Work // Origin: model.FromBlurb
	Skipped    int          // blurbs without a work link (mystery, external works, series bookmarks)
}

// ParseListing parses a listing page. fetchedAt resolves relative blurb dates.
func ParseListing(page []byte, fetchedAt time.Time) (Listing, *Report, error) {
	start := time.Now()
	r := newReport("listing")
	defer func() { r.Duration = time.Since(start) }()
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return Listing{}, r, err
	}
	main, err := r.need("#main", doc.Find("#main"))
	if err != nil {
		return Listing{}, r, err
	}
	l := Listing{Total: -1}
	if h2 := r.sel("#main>h2.heading", main.ChildrenFiltered("h2.heading").First()); h2.Length() > 0 {
		if n, ok := parseTotal(h2.Text()); ok {
			l.Total = n
		}
	}
	r.num("total", l.Total)
	l.Pagination = parsePagination(main, r)
	if l.Blurbs, l.Skipped, err = parseBlurbs(main, fetchedAt, r); err != nil {
		return l, r, err
	}
	// Neither a "N Works" total nor any blurb: the page structure changed (a real empty listing
	// still says "0 Works", or "0 Bookmarks"). Fail instead of silently treating it as empty.
	if l.Total < 0 && len(l.Blurbs) == 0 && l.Skipped == 0 {
		r.Missing = append(r.Missing, "#main>h2.heading total,li.blurb")
		return l, r, unexpected("listing total and blurbs")
	}
	return l, r, nil
}

// minBlurbsForSectionCheck is how many work blurbs a page needs before a section missing from
// all of them fails the parse. Below it, one odd work on a short last page could block a walk.
const minBlurbsForSectionCheck = 5

// parseBlurbs extracts every work blurb under root.
//
// A blurb is skipped only when it has no work link. Any other section can be missing from one
// odd blurb, but when a page of work blurbs has no fandom link in any of them (AO3 requires a
// fandom on every work: work.rb validates fandom_string), or no stats block in any of them,
// the markup changed. Storing such blurbs would empty every listed work's tags and series and
// zero its word count (forcing a refetch of each), so the page fails as unexpected instead.
func parseBlurbs(root *goquery.Selection, fetchedAt time.Time, r *Report) ([]model.Work, int, error) {
	var out []model.Work
	skipped, withFandom, withStats := 0, 0, 0
	r.sel("li.blurb", root.Find("li.blurb")).Each(func(_ int, li *goquery.Selection) {
		// Structural hit counts recorded for every blurb (also lets skeletons be validated).
		r.sel("li.blurb .header.module h4.heading", li.Find("div.header.module h4.heading"))
		stats := r.sel("li.blurb dl.stats", li.Find("dl.stats"))
		w, ok := parseBlurb(li, fetchedAt, r)
		if !ok {
			skipped++
			return
		}
		if stats.Length() > 0 {
			withStats++
		}
		if li.Find("div.header.module h5.fandoms a.tag").Length() > 0 {
			withFandom++
		}
		out = append(out, w)
	})
	r.Counts["blurbs"] = len(out)
	r.Counts["blurbs_skipped"] = skipped
	r.Counts["blurbs_with_fandom"] = withFandom
	r.Counts["blurbs_with_stats"] = withStats
	if len(out) >= minBlurbsForSectionCheck {
		if withFandom == 0 {
			r.Missing = append(r.Missing, "li.blurb h5.fandoms a.tag")
		}
		if withStats == 0 {
			r.Missing = append(r.Missing, "li.blurb dl.stats")
		}
		if withFandom == 0 || withStats == 0 {
			return out, skipped, unexpected("blurb fandoms or stats")
		}
	}
	return out, skipped, nil
}

var workLinkPath = regexp.MustCompile(`(?:^|/)works/([0-9]+)$`)

func workLink(h4 *goquery.Selection) (*goquery.Selection, int64) {
	var link *goquery.Selection
	var id int64
	h4.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		u, err := url.Parse(href)
		if err != nil {
			return true
		}
		if m := workLinkPath.FindStringSubmatch(u.Path); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			link, id = a, n
			return false
		}
		return true
	})
	return link, id
}

func parseBlurb(li *goquery.Selection, fetchedAt time.Time, r *Report) (model.Work, bool) {
	header := li.Find("div.header.module").First()
	h4 := header.Find("h4.heading").First()
	link, id := workLink(h4)
	if link == nil || id == 0 {
		return model.Work{}, false
	}
	w := model.Work{ID: id, Title: clean(link.Text())}
	w.Creators = creators(h4)
	w.Anonymous = header.HasClass("anonymous") || (len(w.Creators) == 0 && strings.Contains(h4.Text(), "Anonymous"))
	w.Restricted = h4.Find(`img[title="Restricted"], img[alt="(Restricted)"]`).Length() > 0

	req := li.Find("ul.required-tags").First()
	r.sel("blurb ul.required-tags", req)
	if t, ok := req.Find(`span[class*="rating-"]`).Attr("title"); ok {
		for _, name := range splitTitle(t) {
			w.Tags = append(w.Tags, model.Tag{Type: model.TagRating, Name: name})
		}
	}
	tags := li.Find("ul.tags").First()
	tags.Find("li.warnings a.tag").Each(func(_ int, a *goquery.Selection) {
		w.Tags = append(w.Tags, tagFromLink(model.TagWarning, a))
	})
	if t, ok := req.Find(`span[class*="category-"]`).Attr("title"); ok {
		for _, name := range splitTitle(t) {
			if !strings.HasPrefix(name, "No ") {
				w.Tags = append(w.Tags, model.Tag{Type: model.TagCategory, Name: name})
			}
		}
	}
	r.sel("blurb h5.fandoms a.tag", header.Find("h5.fandoms a.tag")).Each(func(_ int, a *goquery.Selection) {
		w.Tags = append(w.Tags, tagFromLink(model.TagFandom, a))
	})
	for _, g := range []struct {
		cls string
		typ model.TagType
	}{{"relationships", model.TagRelation}, {"characters", model.TagCharacter}, {"freeforms", model.TagFreeform}} {
		tags.Find("li." + g.cls + " a.tag").Each(func(_ int, a *goquery.Selection) {
			w.Tags = append(w.Tags, tagFromLink(g.typ, a))
		})
	}
	if d := header.Find("p.datetime").First(); d.Length() > 0 {
		if date, approx, ok := parseBlurbDate(d.Text(), fetchedAt); ok {
			w.Revised, w.RevisedApprox = date, approx
		}
	}
	w.SummaryHTML = innerHTML(li.Find("blockquote.userstuff.summary"))

	li.Find("ul.series li").Each(func(_ int, s *goquery.Selection) {
		a := s.Find(`a[href*="/series/"]`).First()
		href, _ := a.Attr("href")
		if sid := pathID(seriesPath, href); sid > 0 {
			w.Series = append(w.Series, model.SeriesPart{ID: sid, Title: clean(a.Text()), Position: atoi(s.Find("strong").First().Text())})
		}
	})

	stats := li.Find("dl.stats").First() // hit count recorded in parseBlurbs
	w.Language = clean(stats.Find("dd.language").Text())
	w.Words = atoi(stats.Find("dd.words").Text())
	w.ChaptersPosted, w.ChaptersExpected = chaptersPair(stats.Find("dd.chapters").Text())
	w.Stats = parseStats(stats)
	w.Complete = complete(w.ChaptersPosted, w.ChaptersExpected)
	r.Counts["blurb_tags"] += len(w.Tags)
	if w.Revised == "" {
		r.Counts["blurb_no_date"]++
	}
	if w.RevisedApprox {
		r.Counts["blurb_relative_date"]++
	}
	return w, true
}

// splitTitle splits get_title_string output ("A, B") into names.
func splitTitle(t string) []string {
	var out []string
	for p := range strings.SplitSeq(t, ", ") {
		if p = clean(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
