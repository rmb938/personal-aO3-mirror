package parse

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestParseTag(t *testing.T) {
	ti, r, err := ParseTag(fixture(t, "tag_character.html"))
	require.NoError(t, err, "missing: %v", r.Missing)
	require.Equal(t, TagInfo{Name: "Fixture A", Type: model.TagCharacter, Canonical: true,
		Parents:  []string{"Fixture Fandom", "Other Fixture Fandom & Co."},
		Synonyms: []string{"FA", "Fixture A (Fixture Fandom)"}, MetaTags: []string{"Fixture A (All Versions)"}}, ti)

	syn, _, err := ParseTag(fixture(t, "tag_synonym.html"))
	require.NoError(t, err)
	require.Equal(t, model.TagFandom, syn.Type)
	require.False(t, syn.Canonical)
	require.Equal(t, "Fixture Fandom", syn.MergerOf)
	require.Equal(t, []string{"Fixture Media"}, syn.Parents)
}
