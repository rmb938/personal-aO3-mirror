// Package parse turns AO3 HTML into model values. Selectors follow the otwarchive view templates
// (app/views/works/_work_module, _meta, show, _preface, chapters/_chapter, series/show).
//
// Parsers never log. They return a content-free Report (selector hit counts, field presence and
// lengths, numbers) that callers log.
package parse

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Version is stored per work; bump it when parsing output changes so `reparse` can be targeted.
const Version = 1

// ErrUnexpected means required structure was missing. The wrapped message names the CSS path only.
var ErrUnexpected = errors.New("parse: unexpected structure")

func unexpected(path string) error { return fmt.Errorf("%w: missing %s", ErrUnexpected, path) }

// Report is a content-free description of one parse, for debug logs.
type Report struct {
	Page      string
	Selectors map[string]int // selector -> match count
	Fields    map[string]any // field -> presence/len/number (never text)
	Counts    map[string]int
	Missing   []string
	Duration  time.Duration
}

func newReport(page string) *Report {
	return &Report{Page: page, Selectors: map[string]int{}, Fields: map[string]any{}, Counts: map[string]int{}}
}

func (r *Report) sel(name string, s *goquery.Selection) *goquery.Selection {
	r.Selectors[name] += s.Length()
	return s
}

func (r *Report) need(name string, s *goquery.Selection) (*goquery.Selection, error) {
	r.sel(name, s)
	if s.Length() == 0 {
		r.Missing = append(r.Missing, name)
		return s, unexpected(name)
	}
	return s, nil
}

// textLen records a text field by length only.
func (r *Report) textLen(field, v string) { r.Fields[field+"_len"] = len(v) }

func (r *Report) num(field string, v int) { r.Fields[field] = v }

// Attrs flattens the report for slog (sorted keys for stable logs).
func (r *Report) Attrs() []any {
	out := []any{"page", r.Page, "parse_ms", r.Duration.Milliseconds()}
	add := func(prefix string, m map[string]int) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, prefix+k, m[k])
		}
	}
	add("sel:", r.Selectors)
	add("n:", r.Counts)
	keys := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, "f:"+k, r.Fields[k])
	}
	if len(r.Missing) > 0 {
		out = append(out, "missing", strings.Join(r.Missing, ","))
	}
	return out
}

// ---- small helpers ----

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

var digits = regexp.MustCompile(`[0-9][0-9,]*`)

// atoi parses "1,234" style numbers; missing => 0.
func atoi(s string) int {
	m := digits.FindString(s)
	if m == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.ReplaceAll(m, ",", ""))
	return n
}

// parseStats reads kudos/hits/comments/bookmarks from a dl.stats (same markup on work pages
// and blurbs).
func parseStats(stats *goquery.Selection) model.Stats {
	return model.Stats{
		Kudos:     atoi(stats.Find("dd.kudos").Text()),
		Hits:      atoi(stats.Find("dd.hits").Text()),
		Comments:  atoi(stats.Find("dd.comments").Text()),
		Bookmarks: atoi(stats.Find("dd.bookmarks").Text()),
	}
}

// chaptersPair parses "3/5", "1,000/?" .
func chaptersPair(s string) (posted, expected int) {
	s = clean(s)
	parts := strings.SplitN(s, "/", 2)
	posted = atoi(parts[0])
	if len(parts) == 2 && !strings.Contains(parts[1], "?") {
		expected = atoi(parts[1])
	}
	return
}

func innerHTML(s *goquery.Selection) string {
	if s.Length() == 0 {
		return ""
	}
	h, err := s.First().Html()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(h)
}

// outerHTMLWithout returns inner HTML of s after removing children matching drop (e.g. headings).
func innerWithout(s *goquery.Selection, drop string) string {
	if s.Length() == 0 {
		return ""
	}
	c := s.First().Clone()
	c.Find(drop).Remove()
	return innerHTML(c)
}

// tagNameFromHref decodes /tags/<param>/works per Tag.from_param.
func tagNameFromHref(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 || segs[0] != "tags" {
		return ""
	}
	return ao3url.TagFromParam(segs[1])
}

// tagFromLink prefers the link text (display name == name), falling back to the href.
func tagFromLink(typ model.TagType, a *goquery.Selection) model.Tag {
	name := clean(a.Text())
	if name == "" {
		href, _ := a.Attr("href")
		name = tagNameFromHref(href)
	}
	return model.Tag{Type: typ, Name: name}
}

var pseudHref = regexp.MustCompile(`^/users/([^/]+)/pseuds/([^/?#]+)`)
var userHref = regexp.MustCompile(`^/users/([^/?#]+)`)

// creators extracts a[rel=author] links: /users/<login>/pseuds/<pseud>.
func creators(s *goquery.Selection) []model.Creator {
	var out []model.Creator
	s.Find(`a[rel="author"]`).Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if u, err := url.Parse(href); err == nil {
			href = u.Path
		}
		if m := pseudHref.FindStringSubmatch(href); m != nil {
			user, _ := url.PathUnescape(m[1])
			pseud, _ := url.PathUnescape(m[2])
			out = append(out, model.Creator{User: user, Pseud: pseud})
		} else if m := userHref.FindStringSubmatch(href); m != nil {
			user, _ := url.PathUnescape(m[1])
			out = append(out, model.Creator{User: user, Pseud: user})
		}
	})
	return out
}

var idInPath = func(prefix string) *regexp.Regexp { return regexp.MustCompile(`^/` + prefix + `/([0-9]+)`) }

var (
	seriesPath   = idInPath("series")
	chapterPath  = regexp.MustCompile(`^/works/[0-9]+/chapters/([0-9]+)`)
	collNamePath = regexp.MustCompile(`^/collections/([^/?#]+)`)
)

func pathID(re *regexp.Regexp, href string) int64 {
	if u, err := url.Parse(href); err == nil {
		href = u.Path
	}
	m := re.FindStringSubmatch(href)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
}

// ---- dates ----

// parseISODate accepts "2026-11-01".
func parseISODate(s string) (string, bool) {
	s = clean(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2006-01-02"), true
	}
	return "", false
}

// relDate matches AO3's own time_ago_in_words (app/helpers/translation_helper.rb), which
// replaces the Rails helper: "less than 1 minute ago", "N minutes ago", "1 hour ago",
// "N hours ago", "1 day ago", "N days ago" (set_format_for_date uses it for at most 30 days).
// "less than 1 minute ago" matches as "1 minute ago", which is the same day.
var relDate = regexp.MustCompile(`(\d+) (minute|hour|day)s? ago`)

// parseBlurbDate handles set_format_for_date (app/helpers/date_helper.rb): Date rfc822
// ("01 Nov 2026", always zero-padded) or, for dates within 30 days, AO3's relative form
// ("3 hours ago", "5 days ago"). approx reports the relative form.
func parseBlurbDate(s string, now time.Time) (date string, approx bool, ok bool) {
	s = clean(s)
	if t, err := time.Parse("02 Jan 2006", s); err == nil {
		return t.Format("2006-01-02"), false, true
	}
	m := relDate.FindStringSubmatch(s)
	if m == nil {
		return "", false, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"minute": time.Minute, "hour": time.Hour, "day": 24 * time.Hour}[m[2]]
	return now.Add(-time.Duration(n) * unit).UTC().Format("2006-01-02"), true, true
}

// ---- pagination ----

// Pagination from will_paginate's ol.pagination.
type Pagination struct {
	Current int
	Last    int
	HasNext bool
}

func parsePagination(doc *goquery.Selection, r *Report) Pagination {
	p := Pagination{Current: 1, Last: 1}
	ol := r.sel("ol.pagination", doc.Find("ol.pagination").First())
	if ol.Length() == 0 {
		return p
	}
	if cur := atoi(ol.Find("span.current, em.current, li .current").First().Text()); cur > 0 {
		p.Current = cur
	}
	ol.Find("li a").Each(func(_ int, a *goquery.Selection) {
		if n := atoi(a.Text()); n > p.Last && clean(a.Text()) == strconv.Itoa(n) {
			p.Last = n
		}
		if rel, _ := a.Attr("rel"); strings.Contains(rel, "next") {
			p.HasNext = true
		}
	})
	if p.Current > p.Last {
		p.Last = p.Current
	}
	r.num("page_current", p.Current)
	r.num("page_last", p.Last)
	return p
}

var totalOf = regexp.MustCompile(`of\s+([0-9][0-9,]*)\s+Works?`)
var totalOnly = regexp.MustCompile(`^\s*([0-9][0-9,]*)\s+Works?`)

// parseTotal reads "1 - 20 of 12,345 Works in ..." / "12 Works in ..." / "1 Work by ...".
func parseTotal(h2 string) (int, bool) {
	h2 = clean(h2)
	if m := totalOf.FindStringSubmatch(h2); m != nil {
		return atoi(m[1]), true
	}
	if m := totalOnly.FindStringSubmatch(h2); m != nil {
		return atoi(m[1]), true
	}
	return 0, false
}
