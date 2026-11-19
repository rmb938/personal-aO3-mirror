package api

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestPathFor(t *testing.T) {
	assert.Equal(t, "/api/v1/sources", pathFor(routeSources))
	assert.Equal(t, "/api/v1/sources/3", pathFor(routeSource, 3))
	assert.Equal(t, "/api/v1/source-adds/12", pathFor(routeSourceAdd, 12))
	assert.Equal(t, "/a/1/b/2", pathFor("/a/{x}/b/{y}", 1, 2))
	assert.Panics(t, func() { pathFor(routeSource) }, "too few ids")
	assert.Panics(t, func() { pathFor(routeSource, 1, 2) }, "too many ids")
	assert.Panics(t, func() { pathFor(routeSources, 1) }, "ids for a route without params")
}

// Every route a Location points at is one the router serves (GET).
func TestPathForMatchesRouter(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	for _, admin := range []bool{false, true} {
		r := a.router(admin)
		for _, p := range []string{pathFor(routeSource, 3), pathFor(routeSourceAdd, 12)} {
			assert.True(t, r.Match(chi.NewRouteContext(), http.MethodGet, p), p)
		}
	}
}
