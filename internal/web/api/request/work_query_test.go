package request

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/searchquery"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestWorkQueryValid(t *testing.T) {
	zero, hi, yes := 0, MaxCount, true
	maxTags := make([]int64, MaxWorkQueryTags)
	for i := range maxTags {
		maxTags[i] = int64(i + 1)
	}
	for _, q := range []WorkQuery{
		{}, // every work
		{TagIDs: maxTags, ExcludeTagIDs: []int64{MaxID}},
		{Complete: &yes, Crossover: new(false), SingleChapter: true, Fetched: &yes},
		{Language: "Fixture Language", Sort: "title", Direction: "asc"},
		{Statuses: []dto.WorkStatus{"ok", "deleted", "hidden", "restricted", "unrevealed"}},
		{Words: IntRange{Min: &zero, Max: &hi}, Kudos: IntRange{Min: &hi}, Hits: IntRange{Max: &zero},
			Comments: IntRange{Min: &zero, Max: &zero}},
		{Revised: DateRange{From: "2025-01-02", To: "2025-01-02"}, Published: DateRange{To: "1999-12-31"}},
		{Q: `lorem -ipsum "dolor sit"`, Sort: "best_match"},
		{Q: strings.Repeat("lorem ", searchquery.MaxTerms), Sort: "kudos"},
		{Q: strings.Repeat("é", MaxQueryRunes)},
	} {
		assert.Empty(t, errorsOf(t, &q), "%+v", q)
	}
}

func TestWorkQueryInvalid(t *testing.T) {
	neg, over, one, two := -1, MaxCount+1, 1, 2
	tooMany := make([]int64, MaxWorkQueryTags+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for name, tc := range map[string]struct {
		in   WorkQuery
		want map[string]string
	}{
		"too many tags": {WorkQuery{TagIDs: tooMany}, map[string]string{"/tag_ids": detailTagIDs.Message()}},
		"repeated tag":  {WorkQuery{ExcludeTagIDs: []int64{3, 3}}, map[string]string{"/exclude_tag_ids": detailTagIDs.Message()}},
		"tag ids out of range": {WorkQuery{TagIDs: []int64{1, 0, MaxID + 1}},
			map[string]string{"/tag_ids/1": detailTagID.Message(), "/tag_ids/2": detailTagID.Message()}},
		"language control": {WorkQuery{Language: "a\nb"}, map[string]string{"/language": detailLanguage.Message()}},
		"language long":    {WorkQuery{Language: strings.Repeat("x", MaxLanguageRunes+1)}, map[string]string{"/language": detailLanguage.Message()}},
		"unknown status": {WorkQuery{Statuses: []dto.WorkStatus{"ok", "OK"}},
			map[string]string{"/statuses/1": detailStatus.Message()}},
		"repeated status": {WorkQuery{Statuses: []dto.WorkStatus{"ok", "ok"}}, map[string]string{"/statuses": detailStatuses.Message()}},
		"counts out of range": {WorkQuery{Words: IntRange{Min: &neg}, Bookmarks: IntRange{Max: &over}},
			map[string]string{"/words/min": detailCount.Message(), "/bookmarks/max": detailCount.Message()}},
		"min above max": {WorkQuery{Hits: IntRange{Min: &two, Max: &one}}, map[string]string{"/hits": detailIntRange.Message()}},
		"bad dates": {WorkQuery{Revised: DateRange{From: "2025-1-02", To: "2025-02-30"}, Published: DateRange{From: "yesterday"}},
			map[string]string{"/revised/from": detailDate.Message(), "/revised/to": detailDate.Message(),
				"/published/from": detailDate.Message()}},
		"from after to": {WorkQuery{Published: DateRange{From: "2025-01-03", To: "2025-01-02"}},
			map[string]string{"/published": detailDates.Message()}},
		"sort and direction": {WorkQuery{Sort: "Words", Direction: "up"},
			map[string]string{"/sort": detailSort.Message(), "/direction": detailDir.Message()}},
		"best match without q": {WorkQuery{Sort: "best_match"}, map[string]string{"/sort": DetailBestMatch.Message()}},
		"q long":               {WorkQuery{Q: strings.Repeat("x", MaxQueryRunes+1)}, map[string]string{"/q": detailQuery.Message()}},
		"q control":            {WorkQuery{Q: "lorem\tipsum"}, map[string]string{"/q": detailQuery.Message()}},
		"q too many terms": {WorkQuery{Q: strings.Repeat("lorem ", searchquery.MaxTerms+1)},
			map[string]string{"/q": detailTerms.Message()}},
		"q only excluded":    {WorkQuery{Q: `-lorem -"ipsum"`}, map[string]string{"/q": detailTerms.Message()}},
		"q no words":         {WorkQuery{Q: `!!! "" -`}, map[string]string{"/q": detailTerms.Message()}},
		"q invalid and sort": {WorkQuery{Q: " ", Sort: "best_match"}, map[string]string{"/q": detailTerms.Message()}},
	} {
		assert.Equal(t, tc.want, errorsOf(t, &tc.in), name)
	}
}

func TestWorkQueryNeedsReveal(t *testing.T) {
	assert.False(t, (&WorkQuery{Sort: "title", TagIDs: []int64{1}}).NeedsReveal(), "structured members only")
	assert.True(t, (&WorkQuery{Language: "x"}).NeedsReveal(), "language is free text")
	assert.True(t, (&WorkQuery{Q: "x"}).NeedsReveal(), "q is free text")
}

func TestParseDate(t *testing.T) {
	d, ok := ParseDate("2025-03-04")
	assert.True(t, ok)
	assert.Equal(t, time.Date(2025, 3, 4, 0, 0, 0, 0, time.UTC), d)
	d, ok = ParseDate("")
	assert.True(t, ok, "an open end")
	assert.True(t, d.IsZero())
	for _, s := range []string{"2025-3-4", "2025-03-04T00:00:00Z", "25-03-04", " 2025-03-04"} {
		_, ok := ParseDate(s)
		assert.False(t, ok, s)
	}
}
