// Package api is the HTTP API served by `ao3mirror serve` (docs/part2-api.md). Handlers call
// controllers and return values; only respond writes JSON.
package api

import (
	"context"
	"errors"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// problemFor maps any handler error to a problem, in one place: *problem.Error as is; store
// not-found sentinels → 404; state conflicts → 409; an existing source → 409 already-exists; a
// cancelled or timed-out request → 503; anything else → 500 internal. internal reports
// whether the error is unexpected (the caller logs it).
func problemFor(err error) (p dto.Problem, internal bool) {
	if e, ok := errors.AsType[*problem.Error](err); ok {
		p = e.Problem()
		return p, p.Type == dto.TypeInternal
	}
	var e *problem.Error
	switch {
	case errors.Is(err, store.ErrNoSource):
		e = problem.WithDetail(dto.TypeNotFound, detailNoSource)
	case errors.Is(err, store.ErrNoJob), errors.Is(err, store.ErrNoSourceAdd):
		e = problem.WithDetail(dto.TypeNotFound, detailNoJob)
	case errors.Is(err, store.ErrNoWork):
		e = problem.WithDetail(dto.TypeNotFound, detailNoWork)
	case errors.Is(err, store.ErrNoSeries), errors.Is(err, store.ErrNoTag), errors.Is(err, store.ErrNoRawPage):
		e = problem.New(dto.TypeNotFound)
	case errors.Is(err, store.ErrStateConflict):
		e = problem.WithDetail(dto.TypeStateConflict, detailStateConflict)
	case isExists(err):
		e = problem.WithDetail(dto.TypeAlreadyExists, detailSourceExists)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		e = problem.WithDetail(dto.TypeUnavailable, detailTimeout)
	default:
		return problem.New(dto.TypeInternal).Problem(), true
	}
	return e.Problem(), false
}

func isExists(err error) bool {
	if errors.Is(err, store.ErrSourceExists) {
		return true
	}
	_, ok := errors.AsType[controllers.ExistsError](err)
	return ok
}
