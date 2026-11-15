package parse

import (
	"reflect"
	"testing"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestParseTag(t *testing.T) {
	ti, r, err := ParseTag(fixture(t, "tag_character.html"))
	if err != nil {
		t.Fatal(err, r.Missing)
	}
	want := TagInfo{Name: "Fixture A", Type: model.TagCharacter, Canonical: true,
		Parents:  []string{"Fixture Fandom", "Other Fixture Fandom & Co."},
		Synonyms: []string{"FA", "Fixture A (Fixture Fandom)"}, MetaTags: []string{"Fixture A (All Versions)"}}
	if !reflect.DeepEqual(ti, want) {
		t.Fatalf("got %+v\nwant %+v", ti, want)
	}
	syn, _, err := ParseTag(fixture(t, "tag_synonym.html"))
	if err != nil {
		t.Fatal(err)
	}
	if syn.Type != model.TagFandom || syn.Canonical || syn.MergerOf != "Fixture Fandom" || !reflect.DeepEqual(syn.Parents, []string{"Fixture Media"}) {
		t.Fatalf("synonym: %+v", syn)
	}
}
