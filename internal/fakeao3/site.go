package fakeao3

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SiteWork is one synthetic work. All text generated from it is fake ("Fixture Work N").
type SiteWork struct {
	ID       int64
	Revised  time.Time
	Kudos    int
	Hits     int
	Words    int
	Chapters int // posted
	Expected int // 0 = "?"
	// Rendered, if set, is how many chapter divs the work page renders (fewer than Chapters
	// simulates a page whose chapters the parser can't all find).
	Rendered int
	Deleted  bool
	Untagged bool // no longer in the tag's listing, but its work page still loads
	Unlinked bool // listed, but its blurb has no work link (as if AO3 changed the blurb markup)
}

// Site is a synthetic AO3 with one tag. It supports the listing params the scraper uses:
// work_search[sort_column|sort_direction|date_from|date_to] and page, plus /works/<id>.
type Site struct {
	mu         sync.Mutex
	Tag        string
	Works      map[int64]*SiteWork
	MaxResults int // AO3 search window; default 100000
	PerPage    int // default 20
}

// NewSite builds n works with ids start..start+n-1, spread over dates, with varied stats.
func NewSite(tag string, start int64, n int) *Site {
	s := &Site{Tag: tag, Works: map[int64]*SiteWork{}, MaxResults: 100000, PerPage: 20}
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		id := start + int64(i)
		s.Works[id] = &SiteWork{
			ID: id, Revised: base.AddDate(0, 0, i*7%2000),
			Kudos: (i*37)%500 + 1, Hits: (i*101)%9000 + 10, Words: 1000 + i*13,
			Chapters: 1 + i%3, Expected: 1 + i%3,
		}
	}
	return s
}

// Update changes a work under lock (e.g. add a chapter) for poll tests.
func (s *Site) Update(id int64, f func(*SiteWork)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.Works[id]; w != nil {
		f(w)
	}
}

// Add inserts a new work.
func (s *Site) Add(w SiteWork) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Works[w.ID] = &w
}

// TagPath is the listing path for the site's tag.
func (s *Site) TagPath() string {
	r := strings.NewReplacer("/", "*s*", "&", "*a*", ".", "*d*", "?", "*q*", "#", "*h*")
	return "/tags/" + strings.ReplaceAll(url.PathEscape(r.Replace(s.Tag)), "%2A", "*") + "/works"
}

// SetSite makes the server answer listing and work requests from site.
func (s *Server) SetSite(site *Site) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.site = site
}

// serveSite returns false if the request is not for the site.
func (s *Site) serve(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.URL.EscapedPath() == s.TagPath() || r.URL.Path == "/tags/"+s.Tag+"/works":
		s.listing(w, r.URL.Query())
		return true
	case strings.HasPrefix(r.URL.Path, "/works/"):
		id, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(r.URL.Path, "/works/"), "/"), 10, 64)
		if err != nil {
			return false
		}
		sw := s.Works[id]
		if sw == nil || sw.Deleted {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, Page(`<h2 class="heading">Error 404</h2>`))
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, s.workPage(sw))
		return true
	}
	return false
}

func (s *Site) listing(w http.ResponseWriter, q url.Values) {
	var list []*SiteWork
	from, _ := time.Parse("2006-01-02", q.Get("work_search[date_from]"))
	to, _ := time.Parse("2006-01-02", q.Get("work_search[date_to]"))
	for _, sw := range s.Works {
		if sw.Deleted || sw.Untagged || (!from.IsZero() && sw.Revised.Before(from)) || (!to.IsZero() && sw.Revised.After(to.Add(24*time.Hour-time.Second))) {
			continue
		}
		list = append(list, sw)
	}
	col, dir := q.Get("work_search[sort_column]"), q.Get("work_search[sort_direction]")
	if col == "" {
		col, dir = "revised_at", "desc"
	}
	key := func(x *SiteWork) int64 {
		switch col {
		case "kudos_count":
			return int64(x.Kudos)
		case "hits":
			return int64(x.Hits)
		case "word_count":
			return int64(x.Words)
		default:
			return x.Revised.Unix()
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := key(list[i]), key(list[j])
		if a == b {
			return list[i].ID > list[j].ID
		}
		if dir == "asc" {
			return a < b
		}
		return a > b
	})
	total := len(list)
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	visible := min(total, s.MaxResults)
	last := (visible + s.PerPage - 1) / s.PerPage
	start := (page - 1) * s.PerPage
	var items []*SiteWork
	if start < visible {
		items = list[start:min(start+s.PerPage, visible)]
	}

	var b strings.Builder
	if last < 2 {
		fmt.Fprintf(&b, `<h2 class="heading">%d Works in <a class="tag" href="%s">Fixture Tag</a></h2>`, total, s.TagPath())
	} else {
		fmt.Fprintf(&b, `<h2 class="heading">%d - %d of %s Works in <a class="tag" href="%s">Fixture Tag</a></h2>`,
			start+1, start+len(items), commas(total), s.TagPath())
	}
	b.WriteString(`<ol class="work index group">`)
	for _, sw := range items {
		b.WriteString(s.blurb(sw))
	}
	b.WriteString(`</ol>`)
	if last > 1 {
		b.WriteString(`<ol class="pagination actions" role="navigation">`)
		for p := 1; p <= last; p++ {
			if p == page {
				fmt.Fprintf(&b, `<li><span class="current">%d</span></li>`, p)
			} else {
				rel := ""
				if p == page+1 {
					rel = ` rel="next"`
				}
				fmt.Fprintf(&b, `<li><a%s href="%s?page=%d">%d</a></li>`, rel, s.TagPath(), p, p)
			}
		}
		b.WriteString(`</ol>`)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, Page(b.String()))
}

func (s *Site) chapters(sw *SiteWork) string {
	exp := "?"
	if sw.Expected > 0 {
		exp = strconv.Itoa(sw.Expected)
	}
	return fmt.Sprintf("%d/%s", sw.Chapters, exp)
}

func (s *Site) blurb(sw *SiteWork) string {
	complete := "complete-no"
	if sw.Expected > 0 && sw.Chapters >= sw.Expected {
		complete = "complete-yes"
	}
	link := fmt.Sprintf(`<a href="/works/%d">Fixture Work %d</a>`, sw.ID, sw.ID)
	if sw.Unlinked {
		link = fmt.Sprintf(`<span>Fixture Work %d</span>`, sw.ID)
	}
	return fmt.Sprintf(`<li id="work_%[1]d" class="work blurb group" role="article">
<div class="header module"><h4 class="heading">%[9]s by <a rel="author" href="/users/fixture_author/pseuds/fixture_author">fixture_author</a></h4>
<h5 class="fandoms heading"><span class="landmark">Fandoms:</span> <a class="tag" href="%[2]s">Fixture Tag</a></h5>
<ul class="required-tags"><li><a href="/help/symbols-key.html"><span class="rating-general-audience rating" title="General Audiences"><span class="text">General Audiences</span></span></a></li><li><a href="/help/symbols-key.html"><span class="%[3]s iswip" title="x"><span class="text">x</span></span></a></li></ul>
<p class="datetime">%[4]s</p></div>
<blockquote class="userstuff summary"><p>Fixture summary %[1]d.</p></blockquote>
<dl class="stats"><dt class="language">Language:</dt><dd class="language">English</dd><dt class="words">Words:</dt><dd class="words">%[5]s</dd>
<dt class="chapters">Chapters:</dt><dd class="chapters">%[6]s</dd><dt class="kudos">Kudos:</dt><dd class="kudos">%[7]d</dd>
<dt class="hits">Hits:</dt><dd class="hits">%[8]d</dd></dl></li>`,
		sw.ID, s.TagPath(), complete, sw.Revised.Format("02 Jan 2006"), commas(sw.Words), s.chapters(sw), sw.Kudos, sw.Hits, link)
}

func (s *Site) workPage(sw *SiteWork) string {
	var ch strings.Builder
	rendered := sw.Chapters
	if sw.Rendered > 0 {
		rendered = sw.Rendered
	}
	for i := 1; i <= rendered; i++ {
		fmt.Fprintf(&ch, `<div class="chapter" id="chapter-%[1]d"><div class="chapter preface group"><h3 class="title"><a href="/works/%[2]d/chapters/%[3]d">Chapter %[1]d</a>: Fixture Chapter</h3></div>
<div class="userstuff module" role="article"><h3 class="landmark heading" id="work">Chapter Text</h3><p>Fixture body %[2]d chapter %[1]d.</p></div></div>`, i, sw.ID, sw.ID*100+int64(i))
	}
	status := ""
	if sw.Chapters > 1 || sw.Expected != 1 {
		status = fmt.Sprintf(`<dt class="status">Updated:</dt><dd class="status">%s</dd>`, sw.Revised.Format("2006-01-02"))
	}
	return Page(fmt.Sprintf(`<div class="work"><div class="wrapper"><dl class="work meta group">
<dt class="rating tags">Rating:</dt><dd class="rating tags"><ul class="commas"><li><a class="tag" href="/tags/General%%20Audiences/works">General Audiences</a></li></ul></dd>
<dt class="fandom tags">Fandom:</dt><dd class="fandom tags"><ul class="commas"><li><a class="tag" href="%[2]s">Fixture Tag</a></li></ul></dd>
<dt class="language">Language:</dt><dd class="language">English</dd>
<dt class="stats">Stats:</dt><dd class="stats"><dl class="stats"><dt class="published">Published:</dt><dd class="published">2019-12-01</dd>%[3]s
<dt class="words">Words:</dt><dd class="words">%[4]s</dd><dt class="chapters">Chapters:</dt><dd class="chapters">%[5]s</dd>
<dt class="kudos">Kudos:</dt><dd class="kudos">%[6]d</dd><dt class="hits">Hits:</dt><dd class="hits">%[7]d</dd></dl></dd></dl></div>
<div id="work-skin" class="wrapper"><div id="workskin"><div class="preface group"><h2 class="title heading">Fixture Work %[1]d</h2>
<h3 class="byline heading"><a rel="author" href="/users/fixture_author/pseuds/fixture_author">fixture_author</a></h3></div>
<div id="chapters" role="article">%[8]s</div></div></div></div>`,
		sw.ID, s.TagPath(), status, commas(sw.Words), s.chapters(sw), sw.Kudos, sw.Hits, ch.String()))
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
