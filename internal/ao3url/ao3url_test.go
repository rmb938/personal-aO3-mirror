package ao3url

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/temoto/robotstxt"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
)

const host = "https://archiveofourown." + "org"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Source
	}{
		{host + "/works/123", Source{Kind: KindWork, Target: "123"}},
		{host + "/works/123/chapters/456?view_adult=true#main", Source{Kind: KindWork, Target: "123"}},
		{"https://www.archiveofourown." + "org/works/9", Source{Kind: KindWork, Target: "9"}},
		{"/series/77", Source{Kind: KindSeries, Target: "77"}},
		{host + "/tags/Fixture%20A*s*Fixture%20B/works", Source{Kind: KindTag, Target: "Fixture A/Fixture B"}},
		{host + "/tags/Fandom%20*a*%20Co*d*", Source{Kind: KindTag, Target: "Fandom & Co."}},
		{host + "/tags/Fixture/works?page=3&work_search%5Bsort_column%5D=hits&work_search%5Blanguage_id%5D=en&work_search%5Bcomplete%5D=T",
			Source{Kind: KindTag, Target: "Fixture", Filters: url.Values{"work_search[language_id]": {"en"}, "work_search[complete]": {"T"}}}},
		{host + "/works?tag_id=Fixture&work_search%5Bcrossover%5D=F&commit=Sort+and+Filter",
			Source{Kind: KindTag, Target: "Fixture", Filters: url.Values{"work_search[crossover]": {"F"}}}},
		{host + "/users/someone", Source{Kind: KindUser, Target: "someone"}},
		{host + "/users/someone/works", Source{Kind: KindUser, Target: "someone"}},
		{host + "/users/someone/pseuds/alias/works", Source{Kind: KindPseud, Target: "someone/alias"}},
		{host + "/users/someone/pseuds/alias", Source{Kind: KindPseud, Target: "someone/alias"}},
		{host + "/users/someone/bookmarks", Source{Kind: KindUserBookmarks, Target: "someone"}},
		{host + "/collections/fixture_coll/works", Source{Kind: KindCollection, Target: "fixture_coll"}},
		{host + "/works?user_id=someone&pseud_id=alias", Source{Kind: KindPseud, Target: "someone/alias"}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for in, want := range map[string]error{
		"https://example.com/works/1": ErrNotAO3,
		"works/1":                     ErrNotAO3,
		host + "/works/search?work_search%5Bquery%5D=x": ErrUnsupported,
		host + "/works?work_search%5Bquery%5D=x":        ErrUnsupported,
		host + "/downloads/1/x.html":                    ErrUnsupported,
		host + "/tags/x/bookmarks":                      ErrUnsupported,
		host + "/":                                      ErrUnsupported,
	} {
		if _, err := Parse(in); !errors.Is(err, want) {
			t.Errorf("Parse(%q) err = %v, want %v", in, err, want)
		}
	}
}

func TestListingPathsAllowedByRobots(t *testing.T) {
	data, err := robotstxt.FromString(fakeao3.Robots)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := ParseOrder("kudos_desc")
	win := &Window{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2020, 12, 31, 0, 0, 0, 0, time.UTC)}
	srcs := []Source{
		{Kind: KindTag, Target: "A/B & C.", Filters: url.Values{"work_search[language_id]": {"en"}}},
		{Kind: KindUser, Target: "someone"},
		{Kind: KindPseud, Target: "someone/alias"},
		{Kind: KindUserBookmarks, Target: "someone"},
		{Kind: KindCollection, Target: "coll"},
		{Kind: KindSeries, Target: "77"},
		{Kind: KindWork, Target: "123"},
	}
	for _, s := range srcs {
		for _, page := range []int{1, 2, 5000} {
			p, err := ListingPath(s, page, o, win)
			if err != nil {
				t.Fatal(err)
			}
			if !data.TestAgent(p, "personal-ao3-mirror") {
				t.Errorf("%s page %d: robots disallows %s", s.Kind, page, p)
			}
			back, err := Parse(p)
			if err != nil || back.Kind != s.Kind || back.Target != s.Target {
				t.Errorf("round trip %s => %+v, %v", p, back, err)
			}
		}
	}
	p, _ := ListingPath(srcs[0], 3, o, win)
	for _, want := range []string{"/tags/A*s*B%20*a*%20C*d*/works?", "page=3", "sort_column%5D=kudos_count", "date_from%5D=2020-01-01", "language_id%5D=en"} {
		if !strings.Contains(p, want) {
			t.Errorf("tag path %s missing %s", p, want)
		}
	}
	if p, _ := ListingPath(srcs[5], 1, o, nil); p != "/series/77" {
		t.Errorf("series ignores order: %s", p)
	}
	if p, _ := ListingPath(srcs[6], 1, o, nil); p != "/works/123?view_full_work=true" {
		t.Errorf("work path: %s", p)
	}
}

func TestFiltersJSONCanonical(t *testing.T) {
	a := url.Values{"b": {"2", "1"}, "a": {"x"}}
	b := url.Values{"a": {"x"}, "b": {"1", "2"}}
	if FiltersJSON(a) != FiltersJSON(b) || FiltersJSON(nil) != "{}" {
		t.Fatalf("%s vs %s", FiltersJSON(a), FiltersJSON(b))
	}
	back, err := ParseFiltersJSON(FiltersJSON(a))
	if err != nil || back.Get("a") != "x" {
		t.Fatal(back, err)
	}
}

func TestParseOrder(t *testing.T) {
	if o, err := ParseOrder("revised_asc"); err != nil || o != (Order{"revised_at", "asc"}) {
		t.Fatal(o, err)
	}
	for _, bad := range []string{"kudos", "kudos_up", "nope_desc", ""} {
		if _, err := ParseOrder(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}
