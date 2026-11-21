package request

import (
	"slices"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

const (
	// MaxCreatorNameQueryRunes caps a creator query's name (AO3 caps logins and pseuds at 40
	// characters each).
	MaxCreatorNameQueryRunes = 128
	// MaxCreatorNameQueryWords caps the words in a creator query's name;
	// model.MaxCreatorNameWords must be the same (a convert test checks).
	MaxCreatorNameQueryWords = 16
)

var (
	detailCreatorName = problem.NewDetail("must be 1 to " + strconv.Itoa(MaxCreatorNameQueryRunes) +
		" characters with no control characters, and 1 to " + strconv.Itoa(MaxCreatorNameQueryWords) + " words")
	detailCreatorSort = problem.OneOf(dto.CreatorSort(""))
)

// CreatorQuery is the body of QUERY /api/v1/creators: a name filter, a sort and a direction.
// Absent members match everything, so `{}` lists every stored creator by name. Paging stays in
// the page and per_page query parameters.
type CreatorQuery struct {
	// Name is words that must each appear in the creator's pseud or login, ignoring ASCII case.
	// It is free text, so it needs the reveal parameter (NeedsReveal).
	Name      string            `json:"name,omitzero"`
	Sort      dto.CreatorSort   `json:"sort,omitzero"`
	Direction dto.SortDirection `json:"direction,omitzero"`
}

// NeedsReveal reports whether the query has a free-text member, which hashed mode refuses
// (reveal-required): matching it would tell a client whether a hidden name holds a guess.
func (q *CreatorQuery) NeedsReveal() bool { return q.Name != "" }

// Validate records every invalid member.
func (q *CreatorQuery) Validate(v *problem.Validation) {
	if q.Name != "" && !validNameWords(q.Name, MaxCreatorNameQueryRunes, MaxCreatorNameQueryWords) {
		v.Member(detailCreatorName, "name")
	}
	if q.Sort != "" && !slices.Contains(q.Sort.EnumValues(), string(q.Sort)) {
		v.Member(detailCreatorSort, "sort")
	}
	if q.Direction != "" && !slices.Contains(q.Direction.EnumValues(), string(q.Direction)) {
		v.Member(detailDir, "direction")
	}
}
