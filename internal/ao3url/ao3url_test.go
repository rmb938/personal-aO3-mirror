package ao3url

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{host + "/users/someone", Source{Kind: KindUser, Target: "someone"}},
		{host + "/users/someone/works", Source{Kind: KindUser, Target: "someone"}},
		{host + "/users/someone/pseuds/alias/works", Source{Kind: KindPseud, Target: "someone/alias"}},
		{host + "/users/someone/pseuds/alias", Source{Kind: KindPseud, Target: "someone/alias"}},
		{host + "/users/someone/bookmarks", Source{Kind: KindUserBookmarks, Target: "someone"}},
		{host + "/collections/fixture_coll/works", Source{Kind: KindCollection, Target: "fixture_coll"}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if assert.NoError(t, err, "Parse(%q)", c.in) {
			assert.Equal(t, c.want, got, "Parse(%q)", c.in)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for in, want := range map[string]error{
		"https://example.com/works/1": ErrNotAO3,
		"works/1":                     ErrNotAO3,
		host + "/works/search?work_search%5Bquery%5D=x": ErrUnsupported,
		host + "/works?work_search%5Bquery%5D=x":        ErrUnsupported,
		// The /works index is never accepted, even with an id (its ids are not decoded like
		// path params, so one tag could become two sources).
		host + "/works?tag_id=Fixture&work_search%5Bcrossover%5D=F": ErrUnsupported,
		host + "/works?user_id=someone&pseud_id=alias":              ErrUnsupported,
		host + "/works?collection_id=fixture_coll":                  ErrUnsupported,
		host + "/downloads/1/x.html":                                ErrUnsupported,
		host + "/tags/x/bookmarks":                                  ErrUnsupported,
		host + "/":                                                  ErrUnsupported,
	} {
		_, err := Parse(in)
		assert.ErrorIs(t, err, want, "Parse(%q)", in)
	}
}

// Errors end up in job last_error and logs, so they never repeat names from the URL.
func TestErrorsAreContentFree(t *testing.T) {
	const sentinel = "SentinelFixtureName"
	for _, in := range []string{
		host + "/tags/" + sentinel + "%zz/works",
		host + "/users/" + sentinel + "%zz",
		host + "/users/x/pseuds/" + sentinel + "%zz",
		host + "/collections/" + sentinel + "%zz",
		"http://[" + sentinel,
	} {
		_, err := Parse(in)
		require.Error(t, err, in)
		require.NotContains(t, err.Error(), sentinel, "Parse error leaked the URL")
	}
	_, err := BasePath(Source{Kind: KindPseud, Target: sentinel})
	require.Error(t, err)
	require.NotContains(t, err.Error(), sentinel, "BasePath error leaked the target")
}

func TestListingPathsAllowedByRobots(t *testing.T) {
	data, err := robotstxt.FromString(fakeao3.Robots)
	require.NoError(t, err)
	o, err := ParseOrder("kudos_desc")
	require.NoError(t, err)
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
			require.NoError(t, err)
			assert.True(t, data.TestAgent(p, "personal-ao3-mirror"), "%s page %d: robots disallows %s", s.Kind, page, p)
			back, err := Parse(p)
			if assert.NoError(t, err, "round trip %s", p) {
				assert.Equal(t, s.Kind, back.Kind, "round trip %s", p)
				assert.Equal(t, s.Target, back.Target, "round trip %s", p)
			}
		}
	}
	p, err := ListingPath(srcs[0], 3, o, win)
	require.NoError(t, err)
	for _, want := range []string{"/tags/A*s*B%20*a*%20C*d*/works?", "page=3", "sort_column%5D=kudos_count", "date_from%5D=2020-01-01", "language_id%5D=en"} {
		assert.Contains(t, p, want, "tag path")
	}
	p, err = ListingPath(srcs[5], 1, o, nil)
	require.NoError(t, err)
	assert.Equal(t, "/series/77", p, "series ignores order")
	p, err = ListingPath(srcs[6], 1, o, nil)
	require.NoError(t, err)
	assert.Equal(t, "/works/123?view_full_work=true", p, "work path")
}

func TestFiltersJSONCanonical(t *testing.T) {
	a := url.Values{"b": {"2", "1"}, "a": {"x"}}
	b := url.Values{"a": {"x"}, "b": {"1", "2"}}
	require.Equal(t, FiltersJSON(a), FiltersJSON(b))
	require.Equal(t, "{}", FiltersJSON(nil))
	back, err := ParseFiltersJSON(FiltersJSON(a))
	require.NoError(t, err)
	require.Equal(t, "x", back.Get("a"))
}

func TestParseOrder(t *testing.T) {
	o, err := ParseOrder("revised_asc")
	require.NoError(t, err)
	require.Equal(t, Order{"revised_at", "asc"}, o)
	for _, bad := range []string{"kudos", "kudos_up", "nope_desc", ""} {
		_, err := ParseOrder(bad)
		assert.Error(t, err, "%q should fail", bad)
	}
}
