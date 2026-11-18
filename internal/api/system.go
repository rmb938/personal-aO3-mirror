package api

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
)

// healthz is GET /api/v1/healthz: the process is up and serving.
func (a *API) healthz(r *http.Request) (any, error) {
	if _, err := parseQuery(r); err != nil {
		return nil, err
	}
	return dto.Health{Status: healthOK}, nil
}

// version is GET /api/v1/version.
func (a *API) version(r *http.Request) (any, error) {
	if _, err := parseQuery(r); err != nil {
		return nil, err
	}
	return dto.Version{Version: a.opts.Version, ParserVersion: parse.Version}, nil
}
