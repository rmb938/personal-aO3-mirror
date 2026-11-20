package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// healthz is GET /api/v1/healthz: the process is up and serving.
func (a *Handlers) Healthz(r *http.Request) (any, error) {
	if err := NoParams(r); err != nil {
		return nil, err
	}
	return dto.Health{Status: dto.HealthOK}, nil
}

// version is GET /api/v1/version.
func (a *Handlers) Version(r *http.Request) (any, error) {
	if err := NoParams(r); err != nil {
		return nil, err
	}
	return dto.Version{Version: a.version, ParserVersion: parse.Version}, nil
}
