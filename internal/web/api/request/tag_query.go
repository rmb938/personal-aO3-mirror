package request

import (
	"slices"
	"strconv"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

const (
	// MaxTagNameQueryRunes caps a tag query's name (AO3 caps new tag names at 150).
	MaxTagNameQueryRunes = 256
	// MaxTagNameQueryWords caps the words in a tag query's name; model.MaxTagNameWords must be
	// the same (a convert test checks).
	MaxTagNameQueryWords = 16
	// MaxTagQueryParents caps parent_ids; model.MaxTagFilterParents must be the same (a convert
	// test checks).
	MaxTagQueryParents = 20
)

var (
	detailTagName = problem.NewDetail("must be 1 to " + strconv.Itoa(MaxTagNameQueryRunes) +
		" characters with no control characters, and 1 to " + strconv.Itoa(MaxTagNameQueryWords) + " words")
	detailTagTypes = problem.NewDetail("must list each tag type at most once")
	detailTagType  = problem.OneOf(dto.TagType(""))
	detailParents  = problem.NewDetail("must be at most " + strconv.Itoa(MaxTagQueryParents) + " distinct ids")
	detailTagSort  = problem.OneOf(dto.TagSort(""))
)

// TagQuery is the body of QUERY /api/v1/tags: AO3 tag search filters, a sort and a direction.
// Absent members match everything, so `{}` lists every stored tag by name. Paging stays in the
// page and per_page query parameters.
type TagQuery struct {
	// Name is words that must each appear in the tag's name, ignoring ASCII case (AO3 name). It
	// is free text, so it needs the reveal parameter (NeedsReveal).
	Name string `json:"name,omitzero"`
	// Types: any of these (AO3 type).
	Types []dto.TagType `json:"types,omitzero"`
	// Canonical: true keeps canonical tags, false tags recorded as not canonical. A tag whose
	// page was never read matches neither.
	Canonical *bool `json:"canonical,omitzero"`
	// ParentIDs must each be a direct parent of the tag (AO3 fandoms, by id).
	ParentIDs []int64           `json:"parent_ids,omitzero"`
	Sort      dto.TagSort       `json:"sort,omitzero"`
	Direction dto.SortDirection `json:"direction,omitzero"`
}

// NeedsReveal reports whether the query has a free-text member, which hashed mode refuses
// (reveal-required): matching it would tell a client whether a hidden name holds a guess.
func (q *TagQuery) NeedsReveal() bool { return q.Name != "" }

// Validate records every invalid member.
func (q *TagQuery) Validate(v *problem.Validation) {
	if q.Name != "" {
		if n := len(strings.Fields(q.Name)); n == 0 || n > MaxTagNameQueryWords ||
			!problem.ValidText(q.Name, MaxTagNameQueryRunes) {
			v.Member(detailTagName, "name")
		}
	}
	values := dto.TagType("").EnumValues()
	seen := make(map[dto.TagType]bool, len(q.Types))
	repeat := false
	for i, t := range q.Types {
		if !slices.Contains(values, string(t)) {
			v.Member(detailTagType, "types", strconv.Itoa(i))
		}
		repeat = repeat || seen[t]
		seen[t] = true
	}
	if repeat {
		v.Member(detailTagTypes, "types")
	}
	validateIDs(v, q.ParentIDs, MaxTagQueryParents, detailParents, "parent_ids")
	if q.Sort != "" && !slices.Contains(q.Sort.EnumValues(), string(q.Sort)) {
		v.Member(detailTagSort, "sort")
	}
	if q.Direction != "" && !slices.Contains(q.Direction.EnumValues(), string(q.Direction)) {
		v.Member(detailDir, "direction")
	}
}
