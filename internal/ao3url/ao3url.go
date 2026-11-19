// Package ao3url maps AO3 URLs to mirror sources and builds robots-allowed listing paths.
//
// Allowed forms we generate (see robots.txt in docs/plan.md):
//
//	/tags/<tag>/works?work_search[...]&page=N
//	/users/<login>/works, /users/<login>/pseuds/<pseud>/works, /users/<login>/bookmarks
//	/collections/<name>/works, /series/<id>, /works/<id>?view_full_work=true
//
// Never generated: /works?... (index with query), /works/search, /downloads/.
package ao3url

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind values match the sources.kind CHECK constraint in meta.db.
type Kind string

const (
	KindTag           Kind = "tag"
	KindUser          Kind = "user"
	KindPseud         Kind = "pseud" // Target "login/pseud"
	KindUserBookmarks Kind = "user_bookmarks"
	KindSeries        Kind = "series"
	KindCollection    Kind = "collection"
	KindWork          Kind = "work"
)

// Kinds lists every Kind.
func Kinds() []Kind {
	return []Kind{KindTag, KindUser, KindPseud, KindUserBookmarks, KindSeries, KindCollection, KindWork}
}

// Source identifies what to mirror. Filters are work_search-style params (never sort/page).
type Source struct {
	Kind    Kind
	Target  string
	Filters url.Values
}

var (
	ErrNotAO3      = errors.New("ao3url: not an AO3 URL")
	ErrUnsupported = errors.New("ao3url: unsupported AO3 URL")
)

// Reason is why Parse rejected a URL. Every Parse error is a *URLError carrying one, so a
// caller (the API) can explain it without parsing the message.
type Reason string

const (
	ReasonNotAO3            Reason = "not_ao3"            // another host, or a relative path
	ReasonMalformed         Reason = "malformed"          // unparseable, or bad percent-encoding
	ReasonChapter           Reason = "chapter"            // /chapters/<id> without the work id
	ReasonTagPage           Reason = "tag_page"           // /tags/<tag>/<not works>
	ReasonPseudBookmarks    Reason = "pseud_bookmarks"    // /users/<login>/pseuds/<pseud>/bookmarks
	ReasonCollectionPage    Reason = "collection_page"    // /collections/<name>/<not works>
	ReasonWorksIndex        Reason = "works_index"        // /works?... (robots-disallowed index)
	ReasonWorkSearch        Reason = "work_search"        // /works/search (robots-disallowed)
	ReasonUnsupportedSource Reason = "unsupported_source" // any other AO3 path
)

// Reasons lists every Reason.
func Reasons() []Reason {
	return []Reason{ReasonNotAO3, ReasonMalformed, ReasonChapter, ReasonTagPage, ReasonPseudBookmarks,
		ReasonCollectionPage, ReasonWorksIndex, ReasonWorkSearch, ReasonUnsupportedSource}
}

// reasonHints is the fixed advice per Reason: what is wrong and what to use instead.
var reasonHints = map[Reason]string{
	ReasonNotAO3:         "use an archiveofourown.org or ao3.org URL, or a path starting with /",
	ReasonMalformed:      "malformed URL",
	ReasonChapter:        "chapter URLs without a work id; open the work and use its /works/<id> URL",
	ReasonTagPage:        "only /tags/<tag> and /tags/<tag>/works are supported",
	ReasonPseudBookmarks: "pseud bookmarks are not supported; use /users/<login>/bookmarks",
	ReasonCollectionPage: "only /collections/<name> and /collections/<name>/works are supported",
	ReasonWorksIndex: "the /works index is not supported; use the tag, user or collection page " +
		"(e.g. /tags/<tag>/works) and filter there",
	ReasonWorkSearch: "work search is disallowed by robots.txt; use a tag page with filters instead",
	ReasonUnsupportedSource: "supported: /tags/<tag>, /users/<login>, /users/<login>/pseuds/<pseud>, " +
		"/users/<login>/bookmarks, /collections/<name>, /series/<id>, /works/<id>",
}

// Hint is r's fixed advice. It never holds input, so callers may show it anywhere.
func (r Reason) Hint() string { return reasonHints[r] }

// URLError is a rejected URL. It never quotes the URL (it holds tag and user names). It
// matches ErrNotAO3 (ReasonNotAO3) or ErrUnsupported (every other reason) with errors.Is.
type URLError struct {
	Reason Reason
	// extra is a content-free addition to the message (the Go type of a parse error).
	extra string
}

func (e *URLError) base() error {
	if e.Reason == ReasonNotAO3 {
		return ErrNotAO3
	}
	return ErrUnsupported
}

func (e *URLError) Error() string {
	msg := e.base().Error() + ": " + e.Reason.Hint()
	if e.extra != "" {
		msg += " (" + e.extra + ")"
	}
	return msg
}

// Is matches ErrNotAO3 or ErrUnsupported.
func (e *URLError) Is(target error) bool { return target == e.base() }

func urlErr(r Reason) error { return &URLError{Reason: r} }

// PerPage is AO3's works-per-listing-page.
const PerPage = 20

// MaxResults is AO3's search window (MAX_SEARCH_RESULTS): deeper pages are unreachable.
const MaxResults = 100000

var hosts = map[string]bool{"archiveofourown.org": true, "www.archiveofourown.org": true, "ao3.org": true, "www.ao3.org": true}

// errMalformed reports a URL that could not be parsed or unescaped. The underlying error is
// dropped on purpose: url.Error and url.EscapeError quote the input, which holds tag/user names
// and ends up in job errors and logs. Only its type is kept.
func errMalformed(err error) error {
	return &URLError{Reason: ReasonMalformed, extra: fmt.Sprintf("%T", err)}
}

// Parse turns any AO3 URL (or bare path) into a Source.
func Parse(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return Source{}, errMalformed(err)
	}
	if u.Host != "" && !hosts[strings.ToLower(u.Hostname())] {
		return Source{}, urlErr(ReasonNotAO3)
	}
	if u.Host == "" && !strings.HasPrefix(u.Path, "/") {
		return Source{}, urlErr(ReasonNotAO3)
	}
	segs := splitPath(u.EscapedPath())
	q := u.Query()
	filters := extractFilters(q)

	switch {
	case len(segs) >= 2 && segs[0] == "works" && isNum(segs[1]):
		return Source{Kind: KindWork, Target: segs[1]}, nil
	case len(segs) >= 2 && segs[0] == "chapters" && isNum(segs[1]):
		return Source{}, urlErr(ReasonChapter)
	case len(segs) >= 2 && segs[0] == "series" && isNum(segs[1]):
		return Source{Kind: KindSeries, Target: segs[1]}, nil
	case len(segs) >= 2 && segs[0] == "tags":
		name, err := decodeTag(segs[1])
		if err != nil {
			return Source{}, err
		}
		if len(segs) > 2 && segs[2] != "works" {
			return Source{}, urlErr(ReasonTagPage)
		}
		return Source{Kind: KindTag, Target: name, Filters: filters}, nil
	case len(segs) >= 2 && segs[0] == "users":
		login, err := url.PathUnescape(segs[1])
		if err != nil {
			return Source{}, errMalformed(err)
		}
		switch {
		case len(segs) >= 4 && segs[2] == "pseuds":
			pseud, err := url.PathUnescape(segs[3])
			if err != nil {
				return Source{}, errMalformed(err)
			}
			if len(segs) > 4 && segs[4] == "bookmarks" {
				return Source{}, urlErr(ReasonPseudBookmarks)
			}
			return Source{Kind: KindPseud, Target: login + "/" + pseud, Filters: filters}, nil
		case len(segs) >= 3 && segs[2] == "bookmarks":
			return Source{Kind: KindUserBookmarks, Target: login}, nil
		case len(segs) == 2 || segs[2] == "works" || segs[2] == "profile":
			return Source{Kind: KindUser, Target: login, Filters: filters}, nil
		}
	case len(segs) >= 2 && segs[0] == "collections":
		name, err := url.PathUnescape(segs[1])
		if err != nil {
			return Source{}, errMalformed(err)
		}
		if len(segs) > 2 && segs[2] != "works" {
			return Source{}, urlErr(ReasonCollectionPage)
		}
		return Source{Kind: KindCollection, Target: name, Filters: filters}, nil
	case len(segs) == 1 && segs[0] == "works":
		// The filtered index (/works?tag_id=X&work_search[...]) is robots-disallowed, and its ids
		// are not decoded like path params, so one tag could become two sources. Not accepted:
		// the same listing is the /tags, /users or /collections page with the same filters.
		return Source{}, urlErr(ReasonWorksIndex)
	case len(segs) >= 2 && segs[0] == "works" && segs[1] == "search":
		return Source{}, urlErr(ReasonWorkSearch)
	}
	return Source{}, urlErr(ReasonUnsupportedSource)
}

func splitPath(p string) []string {
	var out []string
	for s := range strings.SplitSeq(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isNum(s string) bool { _, err := strconv.ParseInt(s, 10, 64); return err == nil }

var tagUnparam = strings.NewReplacer("*s*", "/", "*a*", "&", "*d*", ".", "*q*", "?", "*h*", "#")
var tagParam = strings.NewReplacer("/", "*s*", "&", "*a*", ".", "*d*", "?", "*q*", "#", "*h*")

func decodeTag(seg string) (string, error) {
	dec, err := url.PathUnescape(seg)
	if err != nil {
		return "", errMalformed(err)
	}
	return TagFromParam(dec), nil
}

// TagFromParam reverses Tag.to_param on an already path-unescaped /tags/<param> segment.
func TagFromParam(param string) string { return tagUnparam.Replace(param) }

// TagParam encodes a tag name for a /tags/<param> path segment (Tag.to_param + escaping).
func TagParam(name string) string {
	return strings.ReplaceAll(url.PathEscape(tagParam.Replace(name)), "%2A", "*")
}

// filterKey reports whether a query key is a filter we carry over.
func filterKey(k string) bool {
	switch {
	case k == "work_search[sort_column]", k == "work_search[sort_direction]", k == "page", k == "commit", k == "utf8":
		return false
	case strings.HasPrefix(k, "work_search["), strings.HasPrefix(k, "include_work_search["), strings.HasPrefix(k, "exclude_work_search["):
		return true
	}
	return false
}

func extractFilters(q url.Values) url.Values {
	out := url.Values{}
	for k, vs := range q {
		if !filterKey(k) {
			continue
		}
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				out.Add(k, v)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FiltersJSON is the canonical (sorted) encoding stored in sources.filters_json.
func FiltersJSON(f url.Values) string {
	if len(f) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m := make(map[string][]string, len(f))
	for _, k := range keys {
		vs := append([]string(nil), f[k]...)
		sort.Strings(vs)
		m[k] = vs
	}
	b, _ := json.Marshal(m) // map keys are sorted by encoding/json
	return string(b)
}

// ParseFiltersJSON reverses FiltersJSON.
func ParseFiltersJSON(s string) (url.Values, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string][]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return url.Values(m), nil
}

// ---- ordering ----

// Order is a listing sort: AO3 sort_column + direction.
type Order struct {
	Column    string
	Direction string // "asc" | "desc"
}

var orderColumns = map[string]string{
	"kudos": "kudos_count", "revised": "revised_at", "created": "created_at", "hits": "hits",
	"words": "word_count", "bookmarks": "bookmarks_count", "comments": "comments_count",
	"title": "title_to_sort_on", "author": "authors_to_sort_on",
}

// ParseOrder accepts "<field>_<asc|desc>", e.g. "kudos_desc", "revised_asc".
func ParseOrder(s string) (Order, error) {
	i := strings.LastIndex(s, "_")
	if i <= 0 {
		return Order{}, fmt.Errorf("order %q: want <field>_<asc|desc>", s)
	}
	col, ok := orderColumns[s[:i]]
	dir := s[i+1:]
	if !ok || (dir != "asc" && dir != "desc") {
		names := make([]string, 0, len(orderColumns))
		for k := range orderColumns {
			names = append(names, k)
		}
		sort.Strings(names)
		return Order{}, fmt.Errorf("order %q: field must be one of %s, direction asc|desc", s, strings.Join(names, ","))
	}
	return Order{Column: col, Direction: dir}, nil
}

// OrderNames lists every name ParseOrder accepts ("<field>_<asc|desc>"), sorted.
func OrderNames() []string {
	names := make([]string, 0, 2*len(orderColumns))
	for k := range orderColumns {
		names = append(names, k+"_asc", k+"_desc")
	}
	sort.Strings(names)
	return names
}

// UpdateOrder is used for update polls: most recently revised first.
var UpdateOrder = Order{Column: "revised_at", Direction: "desc"}

// ---- listing paths ----

// Window restricts a listing to a revised_at date range (used to slice >100k sources).
type Window struct {
	From, To time.Time // inclusive dates; zero = open
}

// Sortable reports whether the kind's listing accepts work_search sort params.
func (k Kind) Sortable() bool {
	switch k {
	case KindTag, KindUser, KindPseud, KindCollection:
		return true
	}
	return false
}

// BasePath is the canonical robots-allowed path for a source (no query).
func BasePath(s Source) (string, error) {
	switch s.Kind {
	case KindTag:
		return "/tags/" + TagParam(s.Target) + "/works", nil
	case KindUser:
		return "/users/" + url.PathEscape(s.Target) + "/works", nil
	case KindPseud:
		login, pseud, ok := strings.Cut(s.Target, "/")
		if !ok {
			return "", errors.New("ao3url: pseud target: want login/pseud") // target not quoted: it is a name
		}
		return "/users/" + url.PathEscape(login) + "/pseuds/" + url.PathEscape(pseud) + "/works", nil
	case KindUserBookmarks:
		return "/users/" + url.PathEscape(s.Target) + "/bookmarks", nil
	case KindCollection:
		return "/collections/" + url.PathEscape(s.Target) + "/works", nil
	case KindSeries:
		return "/series/" + s.Target, nil
	case KindWork:
		return "/works/" + s.Target, nil
	}
	return "", fmt.Errorf("unknown source kind %q", s.Kind)
}

// ListingPath builds page N of a source listing with filters, order and optional window.
func ListingPath(s Source, page int, o Order, w *Window) (string, error) {
	base, err := BasePath(s)
	if err != nil {
		return "", err
	}
	if s.Kind == KindWork {
		return WorkPath(s.Target), nil
	}
	q := url.Values{}
	if s.Kind.Sortable() {
		for k, vs := range s.Filters {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		if o.Column != "" {
			q.Set("work_search[sort_column]", o.Column)
			q.Set("work_search[sort_direction]", o.Direction)
		}
		if w != nil {
			if !w.From.IsZero() {
				q.Set("work_search[date_from]", w.From.Format("2006-01-02"))
			}
			if !w.To.IsZero() {
				q.Set("work_search[date_to]", w.To.Format("2006-01-02"))
			}
		}
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return base, nil
	}
	return base + "?" + q.Encode(), nil
}

// WorkPath is the full-work page (all chapters in one request). The view_adult cookie is set
// by internal/httpx, so no view_adult param is needed.
func WorkPath(id string) string { return "/works/" + id + "?view_full_work=true" }

// Pages returns how many listing pages total works need.
func Pages(total int) int {
	if total <= 0 {
		return 0
	}
	return (total + PerPage - 1) / PerPage
}
