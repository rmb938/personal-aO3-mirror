package handlers

import (
	"context"
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

type Handlers struct {
	ctl     *controllers.Controllers
	version string
}

func New(ctl *controllers.Controllers, version string) *Handlers {
	return &Handlers{ctl: ctl, version: version}
}

// IDAction is an admin-socket change to the resource named by the {id} path parameter, with
// no result body (204): POST /api/v1/sources/{id}/confirm|pause|resume, DELETE
// /api/v1/sources/{id}, and the account changes (revoke an invite or session; disable, enable
// or delete a user). A missing resource is 404; a source not in a state the change allows is
// 409 state-conflict.
func (a *Handlers) IDAction(op func(ctx context.Context, id int64) error) runtime.HandlerFunc {
	return func(r *http.Request) (any, error) {
		id, err := runtime.OnlyPathID(r, dto.ParamID)
		if err != nil {
			return nil, err
		}
		if err := op(r.Context(), id); err != nil {
			return nil, err
		}
		return runtime.NoContent(), nil
	}
}
