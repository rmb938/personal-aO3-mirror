// Package convert maps model and controller results to API DTOs, field by field
// (docs/part2-api.md, "Conversion rule"). It exports one function per result, named after it;
// every helper stays unexported. Handlers parse input, call a controller and return
// convert.X(result).
//
// Each model or result has its own file (source.go, source_add.go); this file holds only the
// helpers several of them share.
package convert

import (
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// list converts one page of items with conv.
func list[M, D any](items []M, conv func(M) D, total int, page, perPage int64) dto.List[D] {
	return dto.List[D]{Items: convSlice(items, conv), Page: page, PerPage: perPage, Total: total}
}

// convSlice converts every item with conv; an empty slice gives nil. Converters never need to
// turn nil into an empty list: api.respond marshals with encoding/json/v2, which writes a nil
// slice as [], so lists are never null on the wire.
func convSlice[M, D any](items []M, conv func(M) D) []D {
	if len(items) == 0 {
		return nil
	}
	out := make([]D, len(items))
	for i, it := range items {
		out[i] = conv(it)
	}
	return out
}

// splitError splits a job's last error ("code" or "code:detail") at the first ':'.
func splitError(s string) (dto.JobErrorCode, string) {
	code, detail, _ := strings.Cut(s, ":")
	return dto.JobErrorCode(code), detail
}

// known maps the store's -1 (unknown) to nil.
func known(n int) *int {
	if n < 0 {
		return nil
	}
	return &n
}

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

// convPtr converts the value p points to with conv; nil stays nil.
func convPtr[M, D any](p *M, conv func(M) D) *D {
	if p == nil {
		return nil
	}
	return new(conv(*p))
}

// enumPtr converts a pointer to a string to a pointer to the enum type D; nil stays nil.
func enumPtr[D ~string](p *string) *D {
	return convPtr(p, func(s string) D { return D(s) })
}

// unix maps unix seconds (0: unset) to a UTC time.
func unix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}
