package request

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreatorQueryValid(t *testing.T) {
	for _, q := range []CreatorQuery{
		{}, // every creator
		{Name: "fixture_author pseud", Sort: "works", Direction: "asc"},
		{Name: strings.Repeat("a ", MaxCreatorNameQueryWords), Sort: "name", Direction: "desc"},
		{Name: strings.Repeat("é", MaxCreatorNameQueryRunes)},
	} {
		assert.Empty(t, errorsOf(t, &q), "%+v", q)
	}
}

func TestCreatorQueryInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		in   CreatorQuery
		want map[string]string
	}{
		"name blank":   {CreatorQuery{Name: "   "}, map[string]string{"/name": detailCreatorName.Message()}},
		"name control": {CreatorQuery{Name: "a\nb"}, map[string]string{"/name": detailCreatorName.Message()}},
		"name long": {CreatorQuery{Name: strings.Repeat("x", MaxCreatorNameQueryRunes+1)},
			map[string]string{"/name": detailCreatorName.Message()}},
		"name too wordy": {CreatorQuery{Name: strings.Repeat("a ", MaxCreatorNameQueryWords+1)},
			map[string]string{"/name": detailCreatorName.Message()}},
		"sort and direction": {CreatorQuery{Sort: "uses", Direction: "up"},
			map[string]string{"/sort": detailCreatorSort.Message(), "/direction": detailDir.Message()}},
	} {
		assert.Equal(t, tc.want, errorsOf(t, &tc.in), name)
	}
}

func TestCreatorQueryNeedsReveal(t *testing.T) {
	assert.False(t, (&CreatorQuery{Sort: "works", Direction: "asc"}).NeedsReveal(), "structured members only")
	assert.True(t, (&CreatorQuery{Name: "x"}).NeedsReveal(), "name is free text")
}
