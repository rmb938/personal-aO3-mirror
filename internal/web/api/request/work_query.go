package request

import (
	"slices"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

const (
	// MaxWorkQueryTags caps tag_ids and exclude_tag_ids (each); model.MaxWorkFilterTags must
	// equal it (a convert test checks).
	MaxWorkQueryTags = 20
	// MaxLanguageRunes caps language (AO3's language names are short).
	MaxLanguageRunes = 100
	// MaxID is the largest accepted id: 2^53-1, exact in a JavaScript number.
	MaxID = 1<<53 - 1
	// MaxCount is the largest accepted range bound.
	MaxCount = 1<<31 - 1
	// DateLayout is a range bound's format (YYYY-MM-DD).
	DateLayout = time.DateOnly
)

var (
	detailTagIDs   = problem.NewDetail("must be at most " + strconv.Itoa(MaxWorkQueryTags) + " distinct ids")
	detailTagID    = problem.NewDetail("must be an integer from 1 to " + strconv.FormatInt(MaxID, 10))
	detailStatuses = problem.NewDetail("must list each status at most once")
	detailStatus   = problem.OneOf(dto.WorkStatus(""))
	detailLanguage = problem.NewDetail("must be 1 to " + strconv.Itoa(MaxLanguageRunes) + " characters, with no control characters")
	detailCount    = problem.NewDetail("must be an integer from 0 to " + strconv.Itoa(MaxCount))
	detailIntRange = problem.NewDetail("min must not be above max")
	detailDate     = problem.NewDetail("must be a date, YYYY-MM-DD")
	detailDates    = problem.NewDetail("from must not be after to")
	detailSort     = problem.OneOf(dto.WorkSort(""))
	detailDir      = problem.OneOf(dto.SortDirection(""))
)

// WorkQuery is the body of QUERY /api/v1/works: AO3 work_search filters, a sort and a
// direction. Absent members match everything, so `{}` lists every work like GET. Paging stays
// in the page and per_page query parameters.
type WorkQuery struct {
	// TagIDs must all match, ExcludeTagIDs none (AO3 filter_ids / excluded_tag_ids). A tag id
	// matches works tagged with it, a synonym of it or any of its sub tags.
	TagIDs        []int64 `json:"tag_ids,omitzero"`
	ExcludeTagIDs []int64 `json:"exclude_tag_ids,omitzero"`
	Complete      *bool   `json:"complete,omitzero"`
	Crossover     *bool   `json:"crossover,omitzero"`
	// SingleChapter keeps works expecting one chapter.
	SingleChapter bool `json:"single_chapter,omitzero"`
	// Language is the stored language name, matched exactly. It is free text, so it needs the
	// reveal parameter (NeedsReveal).
	Language string           `json:"language,omitzero"`
	Statuses []dto.WorkStatus `json:"statuses,omitzero"`
	// Fetched: true keeps works whose page was fetched, false works known only from blurbs.
	Fetched   *bool     `json:"fetched,omitzero"`
	Words     IntRange  `json:"words,omitzero"`
	Kudos     IntRange  `json:"kudos,omitzero"`
	Hits      IntRange  `json:"hits,omitzero"`
	Comments  IntRange  `json:"comments,omitzero"`
	Bookmarks IntRange  `json:"bookmarks,omitzero"`
	Revised   DateRange `json:"revised,omitzero"`
	// Published excludes works with no known published date (blurb-only works).
	Published DateRange         `json:"published,omitzero"`
	Sort      dto.WorkSort      `json:"sort,omitzero"`
	Direction dto.SortDirection `json:"direction,omitzero"`
}

// IntRange bounds a counter; both ends are inclusive and an absent end is open.
type IntRange struct {
	Min *int `json:"min,omitzero"`
	Max *int `json:"max,omitzero"`
}

// DateRange bounds a date by day (YYYY-MM-DD); both ends are inclusive and an absent end is
// open.
type DateRange struct {
	From string `json:"from,omitzero"`
	To   string `json:"to,omitzero"`
}

// NeedsReveal reports whether the query has a free-text member, which hashed mode refuses
// (reveal-required): matching it would tell a client whether hidden text equals a guess.
func (w *WorkQuery) NeedsReveal() bool { return w.Language != "" }

// Validate records every invalid member.
func (w *WorkQuery) Validate(v *problem.Validation) {
	validateTagIDs(v, w.TagIDs, "tag_ids")
	validateTagIDs(v, w.ExcludeTagIDs, "exclude_tag_ids")
	if w.Language != "" && !problem.ValidText(w.Language, MaxLanguageRunes) {
		v.Member(detailLanguage, "language")
	}
	validateStatuses(v, w.Statuses)
	w.Words.validate(v, "words")
	w.Kudos.validate(v, "kudos")
	w.Hits.validate(v, "hits")
	w.Comments.validate(v, "comments")
	w.Bookmarks.validate(v, "bookmarks")
	w.Revised.validate(v, "revised")
	w.Published.validate(v, "published")
	if w.Sort != "" && !slices.Contains(w.Sort.EnumValues(), string(w.Sort)) {
		v.Member(detailSort, "sort")
	}
	if w.Direction != "" && !slices.Contains(w.Direction.EnumValues(), string(w.Direction)) {
		v.Member(detailDir, "direction")
	}
}

// validateTagIDs records a list over MaxWorkQueryTags or with a repeat at key, and each id out
// of range at its index.
func validateTagIDs(v *problem.Validation, ids []int64, key string) {
	seen := make(map[int64]bool, len(ids))
	repeat := false
	for i, id := range ids {
		if id < 1 || id > MaxID {
			v.Member(detailTagID, key, strconv.Itoa(i))
		}
		repeat = repeat || seen[id]
		seen[id] = true
	}
	if len(ids) > MaxWorkQueryTags || repeat {
		v.Member(detailTagIDs, key)
	}
}

// validateStatuses records each unknown status at its index, and a repeat at statuses.
func validateStatuses(v *problem.Validation, sts []dto.WorkStatus) {
	values := dto.WorkStatus("").EnumValues()
	seen := make(map[dto.WorkStatus]bool, len(sts))
	repeat := false
	for i, s := range sts {
		if !slices.Contains(values, string(s)) {
			v.Member(detailStatus, "statuses", strconv.Itoa(i))
		}
		repeat = repeat || seen[s]
		seen[s] = true
	}
	if repeat {
		v.Member(detailStatuses, "statuses")
	}
}

func (r IntRange) validate(v *problem.Validation, key string) {
	ok := true
	for _, end := range []struct {
		n   *int
		key string
	}{{r.Min, "min"}, {r.Max, "max"}} {
		if end.n != nil && (*end.n < 0 || *end.n > MaxCount) {
			v.Member(detailCount, key, end.key)
			ok = false
		}
	}
	if ok && r.Min != nil && r.Max != nil && *r.Min > *r.Max {
		v.Member(detailIntRange, key)
	}
}

func (r DateRange) validate(v *problem.Validation, key string) {
	from, fromOK := ParseDate(r.From)
	if !fromOK {
		v.Member(detailDate, key, "from")
	}
	to, toOK := ParseDate(r.To)
	if !toOK {
		v.Member(detailDate, key, "to")
	}
	if fromOK && toOK && !from.IsZero() && !to.IsZero() && from.After(to) {
		v.Member(detailDates, key)
	}
}

// ParseDate parses a range bound (DateLayout, UTC); ok is false for an invalid one. "" is the
// zero time (an open end).
func ParseDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, true
	}
	d, err := time.Parse(DateLayout, s)
	return d, err == nil
}
