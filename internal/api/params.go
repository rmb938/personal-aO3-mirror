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
// middleware validates it). Errors never echo a client-chosen name.
func parseQuery(r *http.Request, allowed ...string) (query, error) {
	vals, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errCode(dto.CodeBadRequest)
	}
	n := 0
	for _, vs := range vals {
		n += len(vs)
	}
	if n > maxQueryParams {
		return nil, errCode(dto.CodeBadRequest)
	}
	q := make(query, len(vals))
	for name, vs := range vals {
		if name != RevealParam && !slices.Contains(allowed, name) {
			return nil, errCode(dto.CodeUnknownParam)
		}
		if len(vs) > 1 {
			return nil, errField(dto.CodeDuplicateParam, name)
		}
		if name != RevealParam {
			q[name] = vs[0]
		}
	}
	return q, nil
}

// int returns parameter name as an integer in [lo, hi], or def when it is absent.
func (q query) int(name string, def, lo, hi int64) (int64, error) {
	s, ok := q[name]
	if !ok {
		return def, nil
	}
	return parseInt(name, s, lo, hi)
}

// parseInt parses s as a base-10 integer (digits only, optional leading '-') in [lo, hi].
func parseInt(field, s string, lo, hi int64) (int64, error) {
	if s == "" || s[0] == '+' {
		return 0, errField(dto.CodeInvalidParam, field)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < lo || v > hi {
		return 0, errField(dto.CodeInvalidParam, field)
	}
	return v, nil
}

// parseID parses an ID (1..MaxID).
func parseID(field, s string) (int64, error) {
	return parseInt(field, s, 1, MaxID)
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
	per, err := q.int("per_page", defaultPerPage, 1, maxPerPage)
	if err != nil {
		return Page{}, err
	}
	pg, err := q.int("page", 1, 1, maxOffset+1)
	if err != nil {
		return Page{}, err
	}
	if pg-1 > maxOffset/per {
		return Page{}, errField(dto.CodeInvalidParam, "page")
	}
	return Page{Page: pg, PerPage: per}, nil
}

// checkText validates a client string: valid UTF-8, no control characters, at most maxRunes
// runes.
func checkText(field, s string, maxRunes int) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return errField(dto.CodeInvalidParam, field)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return errField(dto.CodeInvalidParam, field)
		}
	}
	return nil
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }
