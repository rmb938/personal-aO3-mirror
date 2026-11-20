// Package api assembles the HTTP API routes.
package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/handlers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

type API = runtime.API
type Options = runtime.Options

const RevealParam = runtime.RevealParam

func New(opts Options) (*API, error) {
	opts.RegisterRoutes = registerRoutes
	return runtime.New(opts)
}

func registerRoutes(a *runtime.API, r *chi.Mux, admin bool) error {
	h := handlers.New(a.Controllers(), a.Version())
	r.Get(route.Healthz, a.Handle(h.Healthz))
	r.Get(route.Version, a.Handle(h.Version))
	r.Get(route.Sources, a.Handle(h.ListSources))
	listener := "public"
	if admin {
		listener = "admin"
	}
	r.With(a.SourceAddRateLimit(listener, admin)).Post(route.Sources, a.Handle(h.AddSource(admin)))
	r.Get(route.Source, a.Handle(h.GetSource))
	r.Get(route.SourceAdd, a.Handle(h.GetSourceAdd))
	r.Get(route.Jobs, a.Handle(h.ListJobs))
	r.Get(route.Job, a.Handle(h.GetJob))
	r.Get(route.Status, a.Handle(h.Status))
	if admin {
		r.Post(route.SourceConfirm, a.Handle(h.SourceAction(a.Controllers().ConfirmSource)))
		r.Post(route.SourcePause, a.Handle(h.SourceAction(a.Controllers().PauseSource)))
		r.Post(route.SourceResume, a.Handle(h.SourceAction(a.Controllers().ResumeSource)))
		r.Post(route.SourceCancel, a.Handle(h.CancelSource))
		r.Post(route.SourceRefresh, a.Handle(h.RefreshSource))
		r.Patch(route.Source, a.Handle(h.SetSourceSettings))
		r.Delete(route.Source, a.Handle(h.SourceAction(a.Controllers().RemoveSource)))
		r.Get(route.DebugJob, a.Handle(h.DebugJob))
		r.Get(route.DebugWork, a.Handle(h.DebugWork))
	}
	return nil
}
