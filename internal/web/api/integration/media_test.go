package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// TestListMedia lists the seed's one medium: seedTag's parent, with seedTag as its fandom.
func TestListMedia(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	tag := getJSON[dto.Tag](t, h, route.Path(route.Tag, seedTag))

	l := getJSON[dto.List[dto.Medium]](t, h, route.Media)
	assert.Equal(t, 1, l.Total)
	assert.Equal(t, int64(1), l.Page)
	assert.Equal(t, int64(runtime.DefaultPerPage), l.PerPage)
	require.Len(t, l.Items, 1)
	m := l.Items[0]
	assert.Equal(t, seedMedium, m.ID)
	assert.Equal(t, tag.Parents[0].Tags[0].Name, m.Name, "hashed, the same hash as the tag view's parent")
	assert.True(t, isHash(t, m.Name))
	assert.Nil(t, m.Canonical, "a medium's tag page is never read")
	assert.Equal(t, 1, m.FandomsTotal)
	require.Len(t, m.Fandoms, 1)
	assert.Equal(t, dto.TagListItem{ID: seedTag, Name: tag.Name, Type: "Fandom", Canonical: new(true), Uses: 1,
		InfoFetchedAt: tag.InfoFetchedAt}, m.Fandoms[0])

	// canonical is null on the wire, not left out.
	var raw struct{ Items []map[string]json.RawMessage }
	w := testutil.Get(t, h, route.Media)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.Equal(t, "null", string(raw.Items[0]["canonical"]))

	assert.Empty(t, getJSON[dto.List[dto.Medium]](t, h, route.Media+"?page=2").Items, "past the last page")
	testutil.RequireParamProblem(t, testutil.Get(t, h, route.Media+"?sort=name"), dto.ParamSort, "no sort here")
	testutil.RequireParamProblem(t, testutil.Get(t, h, route.Media+"?per_page=101"), dto.ParamPerPage, "per_page")

	l = getJSON[dto.List[dto.Medium]](t, h, route.Media+revealQuery)
	assert.Equal(t, sentinel+"parent", l.Items[0].Name)
	assert.Equal(t, sentinel+"fandom", l.Items[0].Fandoms[0].Name)
}

func TestListMediumFandoms(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	path := route.Path(route.MediumFandoms, seedMedium)

	for _, q := range []string{"", "?sort=uses", "?sort=name&direction=desc&per_page=1"} {
		l := getJSON[dto.List[dto.TagListItem]](t, h, path+q)
		assert.Equal(t, 1, l.Total, q)
		require.Len(t, l.Items, 1, q)
		assert.Equal(t, seedTag, l.Items[0].ID, q)
		assert.True(t, isHash(t, l.Items[0].Name), q)
	}
	l := getJSON[dto.List[dto.TagListItem]](t, h, path+revealQuery)
	assert.Equal(t, sentinel+"fandom", l.Items[0].Name)

	// Not a medium: an unknown tag, or a tag of another type.
	for _, id := range []int64{999, seedTag} {
		w := testutil.Get(t, h, route.Path(route.MediumFandoms, id))
		require.Equal(t, http.StatusNotFound, w.Code, id)
		assert.Equal(t, runtime.DetailNoMedium.Message(), testutil.DecodeProblem(t, w).Detail, id)
	}

	// Every invalid parameter is listed in one response.
	bad := testutil.Get(t, h, "/api/v1/media/0/fandoms?sort=created_at&direction=up&per_page=0")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())
	var params []dto.ParamName
	for _, e := range testutil.DecodeProblem(t, bad).Errors {
		params = append(params, e.Parameter)
	}
	assert.Equal(t, []dto.ParamName{dto.ParamID, dto.ParamSort, dto.ParamDirection, dto.ParamPerPage}, params)
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?verbose=1"), "", "unknown param")
}
