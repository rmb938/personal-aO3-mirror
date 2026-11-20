// Package api assembles the HTTP API routes.
package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/handlers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

func New(opts runtime.Options, c *controllers.Controllers) (*runtime.API, error) {
	return runtime.New(opts, c, registerRoutes)
}

func registerRoutes(a *runtime.API, c *controllers.Controllers, r *chi.Mux, admin bool) error {
	h := handlers.New(c, a.Version())
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
		r.Post(route.SourceConfirm, a.Handle(h.SourceAction(c.ConfirmSource)))
		r.Post(route.SourcePause, a.Handle(h.SourceAction(c.PauseSource)))
		r.Post(route.SourceResume, a.Handle(h.SourceAction(c.ResumeSource)))
		r.Post(route.SourceCancel, a.Handle(h.CancelSource))
		r.Post(route.SourceRefresh, a.Handle(h.RefreshSource))
		r.Patch(route.Source, a.Handle(h.SetSourceSettings))
		r.Delete(route.Source, a.Handle(h.SourceAction(c.RemoveSource)))
		r.Get(route.DebugJob, a.Handle(h.DebugJob))
		r.Get(route.DebugWork, a.Handle(h.DebugWork))
	}
	return nil
}
