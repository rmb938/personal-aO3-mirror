// Package request holds the API's request body types and their validation. Each type is
// strictly decoded by the api package's decodeJSON and then validated, recording every invalid
// member on the handler's problem.Validation (by JSON Pointer) so the client gets them all in
// one 422.
//
// Like dto, request types never use controllers, store or model types: convert maps a
// validated request to controller options. A request type follows the redact.RegisterTypes
// rules (named fields, json tags, no maps), because the enforcer walks it to check that an
// error's pointer names one of its members.
package request

import "github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"

// Request is a request body. Validate records every invalid member on v (v.Member with the
// json keys leading to it); it never fails fast. It has a pointer receiver, so only a pointer
// to the body (which decoding needs) satisfies Request.
type Request interface {
	Validate(v *problem.Validation)
}

// Types lists every request body type, for the enforcer's pointer check. A test checks that
// each one implements Request.
func Types() []any {
	return []any{
		SourceAdd{},
		SourceRefresh{},
		SourceSettings{},
		WorkQuery{},
		TagQuery{},
		CreatorQuery{},
		CreateInvite{},
		CreateReset{},
		Signup{},
		Reset{},
		ConfirmEnrollment{},
		Login{},
		LoginTOTP{},
		Refresh{},
		ChangePassword{},
		RegenerateRecoveryCodes{},
		Backup{},
	}
}
