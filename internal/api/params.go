package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// RevealParam is the query parameter that asks for real fanwork content on one request. Its
// only accepted value is "1". Go code always uses this constant, never the literal.
const RevealParam = string(dto.ParamReveal)

const (
	// maxQueryParams caps the number of query parameters (values, counting repeats).
	maxQueryParams = 32
	// MaxID is the largest accepted ID: 2^53-1, exact in a JavaScript number.
	MaxID = 1<<53 - 1
	// defaultPerPage and maxPerPage bound list page sizes.
	defaultPerPage = 20
	maxPerPage     = 100
	// maxOffset is AO3's own cap on (page-1)*per_page.
	maxOffset = 100000
)

// params is one request's query and path parameters being validated. Each getter records an
// invalid value in the embedded validation and returns a zero value, so a handler reads every
// parameter, then returns err(): one validation-error listing all of them.
//
//	p, err := parseParams(r, "page", "per_page")
//	if err != nil { return nil, err }
//	pg := p.page()
//	if err := p.err(); err != nil { return nil, err }
type params struct {
	validation
	r    *http.Request
	vals map[string]string // one value per name; RevealParam left out
}

// parseParams parses r's query string. A malformed query or more than maxQueryParams values is
// a bad-request error at once. Names not in allowed (RevealParam is always allowed; the reveal
// middleware validates it) and repeated names are recorded as invalid, in name order.
func parseParams(r *http.Request, allowed ...string) (*params, error) {
	vals, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errDetail(dto.TypeBadRequest, detailMalformedQuery)
	}
	n := 0
	for _, vs := range vals {
		n += len(vs)
	}
	if n > maxQueryParams {
		return nil, errDetail(dto.TypeBadRequest, detailTooManyParams)
	}
	p := &params{r: r, vals: make(map[string]string, len(vals))}
	names := make([]string, 0, len(vals))
	for name := range vals {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		vs := vals[name]
		switch {
		case name != RevealParam && !slices.Contains(allowed, name):
			p.param(name, detailUnknownParam)
		case len(vs) > 1:
			p.param(name, detailDuplicate)
		case name != RevealParam:
			p.vals[name] = vs[0]
		}
	}
	return p, nil
}

// noParams checks that a route without parameters got none (but the reveal param).
func noParams(r *http.Request) error {
	p, err := parseParams(r)
	if err != nil {
		return err
	}
	return p.err()
}

// int returns parameter name as an integer in [1, hi], or def when it is absent. d describes
// the accepted range.
func (p *params) int(name string, def, hi int64, d detail) int64 {
	s, ok := p.vals[name]
	if !ok {
		return def
	}
	v, ok := parseInt(s, hi)
	if !ok {
		p.param(name, d)
	}
	return v
}

// enum returns parameter name when it is one of e's values, or "" when it is absent. d lists
// the values (oneOf(e)).
func (p *params) enum(name string, e interface{ EnumValues() []string }, d detail) string {
	s, ok := p.vals[name]
	if !ok {
		return ""
	}
	if !slices.Contains(e.EnumValues(), s) {
		p.param(name, d)
		return ""
	}
	return s
}

// pathID returns the route's ID path parameter name (1..MaxID).
func (p *params) pathID(name dto.ParamName) int64 {
	v, ok := parseInt(chi.URLParam(p.r, string(name)), MaxID)
	if !ok {
		p.param(string(name), detailID)
	}
	return v
}

// parseInt parses s as a base-10 integer (digits only, optional leading '-') in [1, hi].
func parseInt(s string, hi int64) (int64, bool) {
	if s == "" || s[0] == '+' {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 1 || v > hi {
		return 0, false
	}
	return v, true
}

// Page is a validated page request.
type Page struct {
	Page    int64
	PerPage int64
}

// Offset is the row offset of the page's first item.
func (p Page) Offset() int64 { return (p.Page - 1) * p.PerPage }

// page reads `page` (default 1) and `per_page` (default 20, max 100), and enforces
// (page-1)*per_page <= maxOffset without overflowing (when per_page itself is valid).
func (p *params) page() Page {
	n := len(p.items)
	per := p.int(string(dto.ParamPerPage), defaultPerPage, maxPerPage, detailPerPage)
	m := len(p.items)
	pg := p.int(string(dto.ParamPage), 1, maxOffset+1, detailPage)
	if m == n && len(p.items) == m && pg-1 > maxOffset/per { // both valid: check the cap
		p.param(string(dto.ParamPage), detailPage)
	}
	return Page{Page: pg, PerPage: per}
}

// validText reports whether s is valid UTF-8 with no control characters and at most maxRunes
// runes.
func validText(s string, maxRunes int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }
