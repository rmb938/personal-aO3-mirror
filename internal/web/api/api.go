// Package api assembles the HTTP API routes.
package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/handlers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

func New(opts runtime.Options, log *slog.Logger, c *controllers.Controllers) (*runtime.API, error) {
	return runtime.New(opts, log, c, registerRoutes)
}

func registerRoutes(a *runtime.API, c *controllers.Controllers, r *chi.Mux, admin bool) {
	h := handlers.New(c, a.Version())
	r.Get(route.Healthz, a.Handle(h.Healthz))
	r.Get(route.Version, a.Handle(h.Version))
	r.Get(route.Sources, a.Handle(h.ListSources))
	listener := "public"
	if admin {
		listener = "admin"
	}
	r.With(a.SourceAddRateLimit(listener, admin)).Post(route.Sources, a.Handle(h.AddSource(admin)))
	search := a.SearchRateLimit(listener, admin) // one budget for every QUERY search route
	r.Get(route.Source, a.Handle(h.GetSource))
	r.Get(route.SourceAdd, a.Handle(h.GetSourceAdd))
	r.Get(route.Jobs, a.Handle(h.ListJobs))
	r.Get(route.Job, a.Handle(h.GetJob))
	r.Get(route.Status, a.Handle(h.Status))
	r.With(runtime.AcceptQuery).Get(route.Works, a.Handle(h.ListWorks))
	r.With(runtime.AcceptQuery, search).Query(route.Works, a.Handle(h.QueryWorks))
	r.Get(route.Work, a.Handle(h.GetWork))
	r.Get(route.WorkSkin, a.Handle(h.GetWorkSkin))
	r.Get(route.Chapters, a.Handle(h.GetWorkChapters))
	r.Get(route.Chapter, a.Handle(h.GetChapter))
	r.Get(route.Tag, a.Handle(h.GetTag))
	r.Get(route.TagByName, a.Handle(h.GetTagByName))
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
}
