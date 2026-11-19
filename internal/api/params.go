package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"unicode"
	"unicode/utf8"

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

// query is a request's query parameters after the allowlist checks: one value per name.
type query map[string]string

// parseQuery parses r's query string. It rejects malformed queries, more than maxQueryParams
// values, repeated names, and names not in allowed (RevealParam is always allowed; the reveal
// middleware validates it). Errors name a parameter only when it is a declared dto.ParamName.
func parseQuery(r *http.Request, allowed ...string) (query, error) {
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
	q := make(query, len(vals))
	for name, vs := range vals {
		if name != RevealParam && !slices.Contains(allowed, name) {
			return nil, errParam(name, detailUnknownParam)
		}
		if len(vs) > 1 {
			return nil, errParam(name, detailDuplicate)
		}
		if name != RevealParam {
			q[name] = vs[0]
		}
	}
	return q, nil
}

// int returns parameter name as an integer in [lo, hi], or def when it is absent. d describes
// the accepted range.
func (q query) int(name string, def, lo, hi int64, d detail) (int64, error) {
	s, ok := q[name]
	if !ok {
		return def, nil
	}
	return parseInt(name, s, lo, hi, d)
}

// enum returns parameter name when it is one of e's values, or "" when it is absent. d lists
// the values (oneOf(e)).
func (q query) enum(name string, e interface{ EnumValues() []string }, d detail) (string, error) {
	s, ok := q[name]
	if !ok {
		return "", nil
	}
	if !slices.Contains(e.EnumValues(), s) {
		return "", errParam(name, d)
	}
	return s, nil
}

// parseInt parses s as a base-10 integer (digits only, optional leading '-') in [lo, hi]. d
// describes the accepted range.
func parseInt(field, s string, lo, hi int64, d detail) (int64, error) {
	if s == "" || s[0] == '+' {
		return 0, errParam(field, d)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < lo || v > hi {
		return 0, errParam(field, d)
	}
	return v, nil
}

// parseID parses an ID (1..MaxID).
func parseID(field, s string) (int64, error) {
	return parseInt(field, s, 1, MaxID, detailID)
}

// Page is a validated page request.
type Page struct {
	Page    int64
	PerPage int64
}

// Offset is the row offset of the page's first item.
func (p Page) Offset() int64 { return (p.Page - 1) * p.PerPage }

// page reads `page` (default 1) and `per_page` (default 20, max 100), and enforces
// (page-1)*per_page <= maxOffset without overflowing.
func (q query) page() (Page, error) {
	per, err := q.int(string(dto.ParamPerPage), defaultPerPage, 1, maxPerPage, detailPerPage)
	if err != nil {
		return Page{}, err
	}
	pg, err := q.int(string(dto.ParamPage), 1, 1, maxOffset+1, detailPage)
	if err != nil {
		return Page{}, err
	}
	if pg-1 > maxOffset/per {
		return Page{}, errParam(string(dto.ParamPage), detailPage)
	}
	return Page{Page: pg, PerPage: per}, nil
}

// checkText validates a client string: valid UTF-8, no control characters, at most maxRunes
// runes.
func checkText(field, s string, maxRunes int) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return errParam(field, detailText)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return errParam(field, detailText)
		}
	}
	return nil
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }
