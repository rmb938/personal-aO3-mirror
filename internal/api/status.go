package api

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/api/convert"
)

// status is GET /api/v1/status: the daemon, queue and progress overview.
func (a *API) status(r *http.Request) (any, error) {
	if _, err := parseQuery(r); err != nil {
		return nil, err
	}
	rep, err := a.ctl.Status(r.Context())
	if err != nil {
		return nil, err
	}
	return convert.Status(rep), nil
}
