package integration

import (
	"net/http"
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

// imagePath is GET /api/v1/images/{sha256} for sha (route.Path builds ids only).
func imagePath(sha string) string {
	return strings.Replace(route.Image, "{"+string(dto.ParamSHA256)+"}", sha, 1)
}

// TestGetImage reads seedImageBody, the downloaded image seedFetchedWork embeds.
func TestGetImage(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)
	path := imagePath(seedImageSHA)

	w := testutil.Get(t, h, path+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, seedImageBody, w.Body.Bytes())
	assert.Equal(t, seedImageType, w.Header().Get("Content-Type"))
	assert.Equal(t, strconv.Itoa(len(seedImageBody)), w.Header().Get("Content-Length"))
	assert.Equal(t, runtime.BlobCSP, w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Equal(t, "revealed", w.Header().Get(runtime.HeaderFanwork))
	assert.NotContains(t, logs.String(), sentinel, "image bytes are never logged")

	// Hashed: refused before any lookup, so an unknown hash is refused the same way.
	unknown := strings.Repeat("ab", 32)
	for _, p := range []string{path, imagePath(unknown)} {
		w = testutil.Get(t, h, p)
		require.Equal(t, http.StatusForbidden, w.Code, p)
		prob := testutil.DecodeProblem(t, w)
		assert.Equal(t, dto.TypeRevealRequired, prob.Type)
		assert.Equal(t, runtime.DetailNotJSON.Message(), prob.Detail)
		assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))
		assert.NotEqual(t, runtime.BlobCSP, w.Header().Get("Content-Security-Policy"), "a problem keeps the API's CSP")
	}

	w = testutil.Get(t, h, imagePath(unknown)+revealQuery)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoImage.Message(), testutil.DecodeProblem(t, w).Detail)

	// The hash is checked with the other params, hashed or not.
	for _, bad := range []string{strings.ToUpper(seedImageSHA), seedImageSHA[:63], seedImageSHA + "0",
		strings.Repeat("g", 64)} {
		testutil.RequireParamProblem(t, testutil.Get(t, h, imagePath(bad)), dto.ParamSHA256, bad)
		testutil.RequireParamProblem(t, testutil.Get(t, h, imagePath(bad)+revealQuery), dto.ParamSHA256, bad)
	}
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?page=1"), dto.ParamPage, "no params")
}

// TestWorkImages checks the work view's image refs: hashed unless revealed, the downloaded one's
// sha256 naming GET /api/v1/images/{sha256}.
func TestWorkImages(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)
	path := route.Path(route.Work, seedFetchedWork)

	hashed := getJSON[dto.Work](t, h, path).Images
	require.Len(t, hashed, 2)
	assert.True(t, isHash(t, hashed[0].SrcURL), "hashed")
	assert.True(t, isHash(t, hashed[0].SHA256), "hashed")
	assert.Equal(t, dto.ImageStatus("ok"), hashed[0].Status)
	assert.True(t, isHash(t, hashed[1].SrcURL), "hashed")
	assert.Empty(t, hashed[1].SHA256, "not downloaded")
	assert.Equal(t, dto.ImageStatus("queued"), hashed[1].Status)

	assert.Equal(t, []dto.WorkImage{
		{SrcURL: "https://img.example.test/" + sentinel + "image.png", SHA256: seedImageSHA, Status: "ok"},
		{SrcURL: "https://img.example.test/" + sentinel + "queued.png", Status: "queued"},
	}, getJSON[dto.Work](t, h, path+revealQuery).Images)
	assert.Empty(t, getJSON[dto.Work](t, h, route.Path(route.Work, seedWork)).Images, "a blurb-only work")
}
