package runtime

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// RevealParam is the query parameter that asks for real fanwork content on one request. Its
// only accepted value is "1". Go code always uses this constant, never the literal.
const RevealParam = string(dto.ParamReveal)

const (
	MaxBodyBytes = 16 << 10
	// MaxQueryParams caps the number of query parameters (values, counting repeats).
	MaxQueryParams = 32
	// MaxID is the largest accepted ID: 2^53-1, exact in a JavaScript number.
	MaxID = 1<<53 - 1
	// MaxPosition is the largest accepted chapter position.
	MaxPosition = 100000
	// MaxTagNameRunes caps a tag name path parameter, decoded (AO3 caps new tag names at 150).
	MaxTagNameRunes = 256
	// DefaultPerPage and MaxPerPage bound list page sizes.
	DefaultPerPage = 20
	MaxPerPage     = 100
	// MaxOffset is AO3's own cap on (page-1)*per_page.
	MaxOffset = 100000
)

// params is one request's query and path parameters being validated. Each getter records an
// invalid value in the embedded problem.Validation and returns a zero value, so a handler reads
// every parameter, then returns Err(): one validation-error listing all of them.
//
//	p, err := ParseParams(r, "page", "per_page")
//	if err != nil { return nil, err }
//	pg := p.Page()
//	if err := p.Err(); err != nil { return nil, err }
type Params struct {
	problem.Validation
	r    *http.Request
	vals map[string]string // one value per name; RevealParam left out
}

// ParseParams parses r's query string. A malformed query or more than MaxQueryParams values is
// a bad-request error at once. Names not in allowed (RevealParam is always allowed; the reveal
// middleware validates it) and repeated names are recorded as invalid, in name order.
// ParseParams parses r's query parameters against an allowlist. It performs no I/O beyond
// URL parsing and is safe to call before fetching the request body. It records parameter
// errors in its Validation member, which should be checked via Err() after any further parsing.
// If this call returns a non-nil error, the request is malformed and must be rejected immediately.
func ParseParams(r *http.Request, allowed ...string) (*Params, error) {
	vals, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, problem.WithDetail(dto.TypeBadRequest, DetailMalformedQuery)
	}
	n := 0
	for _, vs := range vals {
		n += len(vs)
	}
	if n > MaxQueryParams {
		return nil, problem.WithDetail(dto.TypeBadRequest, DetailTooManyParams)
	}
	p := &Params{r: r, vals: make(map[string]string, len(vals))}
	names := make([]string, 0, len(vals))
	for name := range vals {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		vs := vals[name]
		switch {
		case name != RevealParam && !slices.Contains(allowed, name):
			p.Param(name, DetailUnknownParam)
		case len(vs) > 1:
			p.Param(name, DetailDuplicate)
		case name != RevealParam:
			p.vals[name] = vs[0]
		}
	}
	return p, nil
}

// NoParams checks that a route without parameters got none (but the reveal param).
func NoParams(r *http.Request) error {
	p, err := ParseParams(r)
	if err != nil {
		return err
	}
	return p.Err()
}

// int returns parameter name as an integer in [1, hi], or def when it is absent. d describes
// the accepted range.
func (p *Params) Int(name string, def, hi int64, d problem.Detail) int64 {
	s, ok := p.vals[name]
	if !ok {
		return def
	}
	v, ok := ParseInt(s, hi)
	if !ok {
		p.Param(name, d)
	}
	return v
}

// enum returns parameter name when it is one of e's values, or "" when it is absent. d lists
// the values (problem.OneOf(e)).
func (p *Params) Enum(name string, e interface{ EnumValues() []string }, d problem.Detail) string {
	s, ok := p.vals[name]
	if !ok {
		return ""
	}
	if !slices.Contains(e.EnumValues(), s) {
		p.Param(name, d)
		return ""
	}
	return s
}

// PathID returns the route's ID path parameter name (1..MaxID).
func (p *Params) PathID(name dto.ParamName) int64 {
	return p.PathInt(name, MaxID, DetailID)
}

// PathInt returns the route's path parameter name as an integer in [1, hi]. d describes the
// accepted range.
func (p *Params) PathInt(name dto.ParamName, hi int64, d problem.Detail) int64 {
	v, ok := ParseInt(chi.URLParam(p.r, string(name)), hi)
	if !ok {
		p.Param(string(name), d)
	}
	return v
}

// PathTagName returns the route's tag name path parameter name, decoded from AO3's /tags/ path
// form (percent escapes, then ao3url.TagFromParam: "*s*" for "/" and so on). It is free text:
// the caller needs the reveal parameter before using it.
func (p *Params) PathTagName(name dto.ParamName) string {
	s := chi.URLParam(p.r, string(name))
	if p.r.URL.RawPath != "" { // chi routed on the escaped path, so the value is still escaped
		var err error
		if s, err = url.PathUnescape(s); err != nil {
			p.Param(string(name), DetailTagName)
			return ""
		}
	}
	s = ao3url.TagFromParam(s)
	if s == "" || !problem.ValidText(s, MaxTagNameRunes) {
		p.Param(string(name), DetailTagName)
		return ""
	}
	return s
}

// onlyPathID parses a route that takes one ID path parameter and no query parameters.
// OnlyPathID parses the single required path parameter ID and validates it, returning an error
// if it's missing or invalid.
func OnlyPathID(r *http.Request, name dto.ParamName) (int64, error) {
	q, err := ParseParams(r)
	if err != nil {
		return 0, err
	}
	id := q.PathID(name)
	return id, q.Err()
}

// parseInt parses s as a base-10 integer (digits only, optional leading '-') in [1, hi].
func ParseInt(s string, hi int64) (int64, bool) {
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
// (page-1)*per_page <= MaxOffset without overflowing (when per_page itself is valid).
func (p *Params) Values() map[string]string {
	return maps.Clone(p.vals)
}

func (p *Params) Page() Page {
	n := p.Len()
	per := p.Int(string(dto.ParamPerPage), DefaultPerPage, MaxPerPage, DetailPerPage)
	m := p.Len()
	pg := p.Int(string(dto.ParamPage), 1, MaxOffset+1, DetailPage)
	if m == n && p.Len() == m && pg-1 > MaxOffset/per { // both valid: check the cap
		p.Param(string(dto.ParamPage), DetailPage)
	}
	return Page{Page: pg, PerPage: per}
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }
