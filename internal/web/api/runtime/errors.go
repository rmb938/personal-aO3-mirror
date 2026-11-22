// Package api is the HTTP API served by `ao3mirror serve` (docs/part2-api.md). Handlers call
// controllers and return values; only respond writes JSON.
package runtime

import (
	"context"
	"errors"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

// problemFor maps any handler error to a problem, in one place: *problem.Error as is; store
// (and controllers) not-found sentinels → 404; state conflicts → 409; a work over the whole-work cap → 422
// work-too-large; an existing source → 409 already-exists; failed credentials or an unusable
// auth token → 401 invalid-credentials; a taken username → 409 already-exists; an auth input
// the controller refuses (username, password policy) → 422 on its member; a busy password
// hasher, a cancelled or timed-out request → 503; anything else → 500 internal. internal reports
// whether the error is unexpected (the caller logs it).
func ProblemFor(err error) (p dto.Problem, internal bool) {
	if e, ok := errors.AsType[*problem.Error](err); ok {
		p = e.Problem()
		return p, p.Type == dto.TypeInternal
	}
	if e := authMemberError(err); e != nil {
		return e.Problem(), false
	}
	var e *problem.Error
	switch {
	case errors.Is(err, store.ErrNoSource):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoSource)
	case errors.Is(err, store.ErrNoJob), errors.Is(err, store.ErrNoSourceAdd):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoJob)
	case errors.Is(err, store.ErrNoWork):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoWork)
	case errors.Is(err, store.ErrNoChapter):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoChapter)
	case errors.Is(err, controllers.ErrWorkTooLarge):
		e = problem.WithDetail(dto.TypeWorkTooLarge, DetailWorkTooLarge)
	case errors.Is(err, store.ErrNoTag):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoTag)
	case errors.Is(err, controllers.ErrNoMedium):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoMedium)
	case errors.Is(err, store.ErrNoSeries):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoSeries)
	case errors.Is(err, store.ErrNoCreator):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoCreator)
	case errors.Is(err, store.ErrNoCollection):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoCollection)
	case errors.Is(err, store.ErrNoImage):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoImage)
	case errors.Is(err, store.ErrNoUser):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoUser)
	case errors.Is(err, store.ErrNoInvite):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoInvite)
	case errors.Is(err, store.ErrNoSession):
		e = problem.WithDetail(dto.TypeNotFound, DetailNoSession)
	case errors.Is(err, controllers.ErrInvalidCredentials):
		e = problem.WithDetail(dto.TypeInvalidCredentials, DetailInvalidCredentials)
	case errors.Is(err, controllers.ErrInvalidToken):
		e = problem.WithDetail(dto.TypeInvalidCredentials, DetailInvalidToken)
	case errors.Is(err, controllers.ErrUsernameTaken):
		e = problem.WithDetail(dto.TypeAlreadyExists, DetailUsernameTaken)
	case errors.Is(err, auth.ErrHasherBusy):
		e = problem.WithDetail(dto.TypeUnavailable, DetailBusy)
	case errors.As(err, new(controllers.RateLimitedError)):
		e = problem.WithDetail(dto.TypeRateLimited, DetailLoginBackoff)
	case errors.Is(err, store.ErrNoRawPage):
		e = problem.New(dto.TypeNotFound)
	case errors.Is(err, store.ErrStateConflict):
		e = problem.WithDetail(dto.TypeStateConflict, DetailStateConflict)
	case isExists(err):
		e = problem.WithDetail(dto.TypeAlreadyExists, DetailSourceExists)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		e = problem.WithDetail(dto.TypeUnavailable, DetailTimeout)
	default:
		return problem.New(dto.TypeInternal).Problem(), true
	}
	return e.Problem(), false
}

// authMemberDetails maps the Auth controller's input errors to the body member they concern
// and its detail. Every auth body names its (new) password "password".
var authMemberDetails = []struct {
	err    error
	member string
	detail problem.Detail
}{
	{controllers.ErrInvalidUsername, "username", request.DetailUsername},
	{controllers.ErrResetPassword, "password", DetailResetPassword},
	{auth.ErrPasswordInvalid, "password", DetailPasswordInvalid},
	{auth.ErrPasswordTooShort, "password", DetailPasswordTooShort},
	{auth.ErrPasswordTooLong, "password", DetailPasswordTooLong},
	{auth.ErrPasswordIsUsername, "password", DetailPasswordIsUsername},
	{auth.ErrPasswordCommon, "password", DetailPasswordCommon},
}

// authMemberError is err as a validation error on its body member, or nil when it is not
// one of authMemberDetails.
func authMemberError(err error) *problem.Error {
	for _, m := range authMemberDetails {
		if errors.Is(err, m.err) {
			var v problem.Validation
			v.Member(m.detail, m.member)
			e, _ := errors.AsType[*problem.Error](v.Err())
			return e
		}
	}
	return nil
}

func isExists(err error) bool {
	if errors.Is(err, store.ErrSourceExists) {
		return true
	}
	_, ok := errors.AsType[controllers.ExistsError](err)
	return ok
}
