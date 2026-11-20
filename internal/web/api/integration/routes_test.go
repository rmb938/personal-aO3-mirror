package integration

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
)

// Every route a Location points at is one the router serves (GET).
func TestPathMatchesRouter(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	for _, admin := range []bool{false, true} {
		r := a.Router(admin)
		for _, p := range []string{route.Path(route.Source, 3), route.Path(route.SourceAdd, 12)} {
			assert.True(t, r.Match(chi.NewRouteContext(), http.MethodGet, p), p)
		}
	}
}
