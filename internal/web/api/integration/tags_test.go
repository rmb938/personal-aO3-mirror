package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestGetTag(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	tag := getJSON[dto.Tag](t, h, "/api/v1/tags/3")
	assert.Equal(t, seedTag, tag.ID)
	assert.Equal(t, dto.TagType("Fandom"), tag.Type)
	assert.True(t, isHash(t, tag.Name), "tag name hashed")
	work := getJSON[dto.Work](t, h, "/api/v1/works/9")
	assert.Equal(t, work.Tags[1].Name, tag.Name, "the hash matches the work's tag in one key epoch")
	assert.Equal(t, new(true), tag.Canonical)
	assert.Nil(t, tag.Merger)
	assert.Equal(t, 1, tag.WorksCount)
	assert.Equal(t, seedTime, tag.InfoFetchedAt)
	assert.Empty(t, tag.Children)
	assert.Empty(t, tag.SubTags)
	for name, groups := range map[string][]dto.TagGroup{
		"parents": tag.Parents, "meta_tags": tag.MetaTags, "synonyms": tag.Synonyms,
	} {
		require.Len(t, groups, 1, name)
		require.Len(t, groups[0].Tags, 1, name)
		assert.Equal(t, 1, groups[0].Total, name)
		assert.True(t, isHash(t, groups[0].Tags[0].Name), name+" name hashed")
	}
	assert.Equal(t, dto.TagType("Media"), tag.Parents[0].Type, "a fandom's parents are media")
	assert.Equal(t, dto.TagType("Fandom"), tag.Synonyms[0].Type, "a synonym takes the tag's type")

	// The synonym: merged into the seeded tag, so not canonical.
	syn := getJSON[dto.Tag](t, h, "/api/v1/tags/"+strconv.FormatInt(tag.Synonyms[0].Tags[0].ID, 10))
	require.NotNil(t, syn.Merger)
	assert.Equal(t, dto.TagRef{ID: tag.ID, Name: tag.Name, Type: "Fandom"}, *syn.Merger)
	assert.Equal(t, new(false), syn.Canonical)

	// A tag never read from its tag page: canonical null, no fetch time, lists [].
	var raw map[string]json.RawMessage
	b := testutil.Get(t, h, "/api/v1/tags/1")
	require.Equal(t, http.StatusOK, b.Code)
	require.NoError(t, json.Unmarshal(b.Body.Bytes(), &raw))
	assert.Equal(t, "null", string(raw["canonical"]))
	assert.Equal(t, "null", string(raw["merger"]))
	assert.Equal(t, "[]", string(raw["parents"]))
	assert.NotContains(t, raw, "info_fetched_at")

	nf := testutil.Get(t, h, "/api/v1/tags/999")
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoTag.Message(), testutil.DecodeProblem(t, nf).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/tags/x"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/tags/3?verbose=1"), "", "unknown param")

	// Revealed: the real content.
	tag = getJSON[dto.Tag](t, h, "/api/v1/tags/3"+revealQuery)
	assert.Equal(t, sentinel+"fandom", tag.Name)
	assert.Equal(t, sentinel+"synonym", tag.Synonyms[0].Tags[0].Name)
}

func TestGetTagByName(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	path := "/api/v1/tags/by-name/" + sentinel + "fandom"

	// Hashed: the name is free text, refused before the store is read.
	w := testutil.Get(t, h, path)
	require.Equal(t, http.StatusBadRequest, w.Code)
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeRevealRequired, p.Type)
	assert.Equal(t, runtime.DetailFreeTextPath.Message(), p.Detail)
	testutil.AssertNoSentinel(t, w, logs, sentinel)

	// An invalid name or param is a validation error, hashed too.
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/tags/by-name/a%01b"), dto.ParamTagName, "control character")
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?verbose=1"), "", "unknown param")

	// Revealed: case-insensitive, as AO3's /tags/{name}.
	tag := getJSON[dto.Tag](t, h, "/api/v1/tags/by-name/sentinel-FANDOM"+revealQuery)
	assert.Equal(t, seedTag, tag.ID)
	assert.Equal(t, sentinel+"fandom", tag.Name)
	assert.NotContains(t, logs.String(), sentinel, "the name is never logged")

	nf := testutil.Get(t, h, "/api/v1/tags/by-name/"+sentinel+"missing"+revealQuery)
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoTag.Message(), testutil.DecodeProblem(t, nf).Detail)
}

// Requests refused before the handler (wrong method, URL too long) never log the name either.
func TestGetTagByNameRefusedNotLogged(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	path := "/api/v1/tags/by-name/" + sentinel + "fandom"

	w := testutil.Do(t, h, httptest.NewRequest(http.MethodPost, path, nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	testutil.AssertNoSentinel(t, w, logs, sentinel)

	logs.Reset()
	w = testutil.Get(t, h, path+strings.Repeat("x", runtime.MaxURLBytes))
	require.Equal(t, http.StatusRequestURITooLong, w.Code)
	testutil.AssertNoSentinel(t, w, logs, sentinel)
	assert.Equal(t, route.TagByName, testutil.AccessLine(t, logs)["route"], "prerouted, logged by pattern")
}

func TestTagWorks(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.SearchPerMin = 2 })
	h := a.Public()
	fandom := "/api/v1/tags/" + strconv.FormatInt(seedTag, 10) + "/works"
	blurb := getJSON[dto.Work](t, h, "/api/v1/works/"+strconv.FormatInt(seedWork, 10))
	require.Len(t, blurb.Tags, 1)
	freeform := strconv.FormatInt(blurb.Tags[0].ID, 10)

	g := testutil.Get(t, h, fandom)
	require.Equal(t, http.StatusOK, g.Code)
	assert.Equal(t, runtime.ContentTypeJSON, g.Header().Get(runtime.HeaderAcceptQuery), "GET advertises QUERY")
	l := getJSON[dto.List[dto.WorkBlurb]](t, h, fandom+"?per_page=1")
	assert.Equal(t, []int64{seedFetchedWork}, workIDs(t, l), "only the tag's works")
	assert.Equal(t, getJSON[dto.Tag](t, h, "/api/v1/tags/3").WorksCount, l.Total, "the tag view's count")
	assert.Equal(t, int64(1), l.PerPage)
	assert.Empty(t, workIDs(t, getJSON[dto.List[dto.WorkBlurb]](t, h, fandom+"?page=2")))

	// QUERY: the body's filters on top of the path tag.
	assert.Equal(t, []int64{seedFetchedWork}, workIDs(t, queryWorks(t, h, fandom, `{"fetched":true}`)))
	assert.Empty(t, workIDs(t, queryWorks(t, h, fandom, `{"tag_ids":[`+freeform+`]}`)), "both tags must match")

	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/tags/x/works"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, fandom+"?verbose=1"), "", "unknown param")
	testutil.RequireParamProblem(t, testutil.Get(t, h, fandom+"?sort=best_match"), dto.ParamSort, "best_match without q")
	testutil.RequireParamProblem(t, queryJSON(t, a.Admin(), "/api/v1/tags/0/works", `{}`), dto.ParamID, "query bad id")
	bad := testutil.Get(t, h, "/api/v1/tags/0/works?sort=x")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code)
	assert.Len(t, testutil.DecodeProblem(t, bad).Errors, 2, "the id and the sort in one response")

	nf := testutil.Get(t, h, "/api/v1/tags/999/works")
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoTag.Message(), testutil.DecodeProblem(t, nf).Detail)

	// QUERY shares the search budget with QUERY /works (2 per minute here, both spent above).
	w := queryJSON(t, h, "/api/v1/works", `{}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

// queryTags sends a tag query, requires 200 and decodes the page.
func queryTags(t *testing.T, h http.Handler, target, body string) dto.List[dto.TagListItem] {
	t.Helper()
	w := queryJSON(t, h, target, body)
	require.Equal(t, http.StatusOK, w.Code, body+": "+w.Body.String())
	assert.Equal(t, runtime.ContentTypeJSON, w.Header().Get(runtime.HeaderAcceptQuery), "QUERY advertised")
	var l dto.List[dto.TagListItem]
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &l))
	return l
}

func tagIDs(l dto.List[dto.TagListItem]) []int64 {
	ids := []int64{}
	for _, it := range l.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// TestQueryTags runs tag queries over the seed: seedTag (a canonical fandom with a parent, a
// meta tag and a synonym) among the seeded works' other tags.
func TestQueryTags(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	tag := getJSON[dto.Tag](t, h, "/api/v1/tags/"+strconv.FormatInt(seedTag, 10))
	require.Len(t, tag.Parents, 1)
	require.Len(t, tag.Synonyms, 1)
	parent := tag.Parents[0].Tags[0].ID
	synonym := tag.Synonyms[0].Tags[0].ID

	all := queryTags(t, h, "/api/v1/tags", `{}`)
	assert.Greater(t, all.Total, 4, "the works' tags and the tag page's relations")
	assert.Len(t, all.Items, all.Total)
	for _, it := range all.Items {
		assert.True(t, isHash(t, it.Name), "names hashed")
	}

	for body, want := range map[string][]int64{
		`{"types":["Fandom"],"canonical":true}`:                   {seedTag},
		`{"canonical":false}`:                                     {synonym},
		`{"parent_ids":[` + strconv.FormatInt(parent, 10) + `]}`:  {seedTag},
		`{"parent_ids":[` + strconv.FormatInt(seedTag, 10) + `]}`: {},
	} {
		l := queryTags(t, h, "/api/v1/tags", body)
		assert.Equal(t, want, tagIDs(l), body)
		assert.Equal(t, len(want), l.Total, body)
	}

	// The synonym: merged into seedTag, not canonical, used by no stored work.
	syn := queryTags(t, h, "/api/v1/tags", `{"canonical":false}`).Items[0]
	assert.Equal(t, dto.TagListItem{ID: synonym, Name: syn.Name, Type: "Fandom", Canonical: new(false),
		MergerID: new(seedTag)}, syn)
	fandom := queryTags(t, h, "/api/v1/tags", `{"types":["Fandom"],"canonical":true}`).Items[0]
	assert.Equal(t, 1, fandom.Uses, "seedFetchedWork")
	assert.Equal(t, tag.InfoFetchedAt, fandom.InfoFetchedAt)

	// By uses, most used first; paging is in the query string.
	byUses := queryTags(t, h, "/api/v1/tags?per_page=2&page=1", `{"sort":"uses"}`)
	assert.Equal(t, all.Total, byUses.Total)
	require.Len(t, byUses.Items, 2)
	assert.GreaterOrEqual(t, byUses.Items[0].Uses, byUses.Items[1].Uses)
	asc := queryTags(t, h, "/api/v1/tags?per_page=1", `{"sort":"uses","direction":"asc"}`)
	assert.Equal(t, 0, asc.Items[0].Uses, "a relation no stored work uses")

	// Every invalid parameter and member is listed in one response.
	bad := queryJSON(t, h, "/api/v1/tags?per_page=0", `{"types":["fandom"],"parent_ids":[0],"sort":"created_at"}`)
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())
	var where []string
	for _, e := range testutil.DecodeProblem(t, bad).Errors {
		where = append(where, string(e.Parameter)+e.Pointer)
	}
	assert.Equal(t, []string{"per_page", "/types/0", "/parent_ids/0", "/sort"}, where,
		"pointers pass the enforcer (they name TagQuery members)")

	// The name is free text: refused hashed, before the store is read.
	logs.Reset()
	w := queryJSON(t, h, "/api/v1/tags", `{"name":"`+sentinel+`fandom"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeRevealRequired, p.Type)
	assert.Equal(t, runtime.DetailFreeText.Message(), p.Detail)
	testutil.AssertNoSentinel(t, w, logs, sentinel)
	assert.Equal(t, dto.TypeValidation, testutil.DecodeProblem(t, queryJSON(t, h, "/api/v1/tags", `{"name":" "}`)).Type,
		"an invalid name is a validation error, hashed too")

	// Revealed: name words match anywhere, ignoring case.
	l := queryTags(t, h, "/api/v1/tags"+revealQuery, `{"name":"FANDOM sentinel-"}`)
	assert.Equal(t, []int64{seedTag}, tagIDs(l))
	assert.Equal(t, sentinel+"fandom", l.Items[0].Name)
	assert.Empty(t, queryTags(t, h, "/api/v1/tags"+revealQuery, `{"name":"`+sentinel+`fandom nomatch"}`).Items,
		"every word must match")
	assert.NotContains(t, logs.String(), sentinel, "the name is never logged")
}

func TestQueryTagsRateLimit(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.SearchPerMin = 1 })
	queryWorks(t, a.Public(), "/api/v1/works", `{}`)
	w := queryJSON(t, a.Public(), "/api/v1/tags", `{}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code, "one search budget with QUERY /works")
	assert.Equal(t, dto.TypeRateLimited, testutil.DecodeProblem(t, w).Type)
}
