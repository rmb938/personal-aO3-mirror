// Package api assembles the HTTP API routes.
package api

import (
	"errors"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/handlers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// New builds the API over c, whose Auth controller (the admin socket's account routes) is
// required.
func New(opts runtime.Options, log *slog.Logger, c *controllers.Controllers) (*runtime.API, error) {
	if c.Auth() == nil {
		return nil, errors.New("api: no Auth controller")
	}
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
	r.With(runtime.AcceptQuery, search).Query(route.Tags, a.Handle(h.QueryTags))
	r.Get(route.Tag, a.Handle(h.GetTag))
	r.With(runtime.AcceptQuery).Get(route.TagWorks, a.Handle(h.ListTagWorks))
	r.With(runtime.AcceptQuery, search).Query(route.TagWorks, a.Handle(h.QueryTagWorks))
	r.Get(route.TagByName, a.Handle(h.GetTagByName))
	r.Get(route.Media, a.Handle(h.ListMedia))
	r.Get(route.MediumFandoms, a.Handle(h.ListMediumFandoms))
	r.Get(route.Series, a.Handle(h.GetSeries))
	r.Get(route.SeriesWorks, a.Handle(h.ListSeriesWorks))
	r.With(runtime.AcceptQuery, search).Query(route.Creators, a.Handle(h.QueryCreators))
	r.Get(route.Creator, a.Handle(h.GetCreator))
	r.With(runtime.AcceptQuery).Get(route.CreatorWorks, a.Handle(h.ListCreatorWorks))
	r.With(runtime.AcceptQuery, search).Query(route.CreatorWorks, a.Handle(h.QueryCreatorWorks))
	r.Get(route.Collection, a.Handle(h.GetCollection))
	r.With(runtime.AcceptQuery).Get(route.CollectionWorks, a.Handle(h.ListCollectionWorks))
	r.With(runtime.AcceptQuery, search).Query(route.CollectionWorks, a.Handle(h.QueryCollectionWorks))
	r.Get(route.Image, a.Handle(h.GetImage))

	signup := a.SignupRateLimit(listener, admin) // one budget for both steps of signup and reset
	r.With(signup).Post(route.AuthSignup, a.Handle(h.Signup))
	r.With(signup).Post(route.AuthSignupConfirm, a.Handle(h.ConfirmSignup))
	r.With(signup).Post(route.AuthReset, a.Handle(h.StartReset))
	r.With(signup).Post(route.AuthResetConfirm, a.Handle(h.ConfirmReset))
	if admin {
		r.Post(route.SourceConfirm, a.Handle(h.IDAction(c.ConfirmSource)))
		r.Post(route.SourcePause, a.Handle(h.IDAction(c.PauseSource)))
		r.Post(route.SourceResume, a.Handle(h.IDAction(c.ResumeSource)))
		r.Post(route.SourceCancel, a.Handle(h.CancelSource))
		r.Post(route.SourceRefresh, a.Handle(h.RefreshSource))
		r.Patch(route.Source, a.Handle(h.SetSourceSettings))
		r.Delete(route.Source, a.Handle(h.IDAction(c.RemoveSource)))
		r.Get(route.DebugJob, a.Handle(h.DebugJob))
		r.Get(route.DebugWork, a.Handle(h.DebugWork))

		ac := c.Auth()
		r.Post(route.AdminInvites, a.Handle(h.CreateInvite))
		r.Get(route.AdminInvites, a.Handle(h.ListInvites))
		r.Delete(route.AdminInvite, a.Handle(h.IDAction(ac.RevokeInvite)))
		r.Get(route.AdminUsers, a.Handle(h.ListUsers))
		r.Delete(route.AdminUser, a.Handle(h.IDAction(ac.DeleteUser)))
		r.Post(route.AdminUserDisable, a.Handle(h.IDAction(ac.DisableUser)))
		r.Post(route.AdminUserEnable, a.Handle(h.IDAction(ac.EnableUser)))
		r.Post(route.AdminUserReset, a.Handle(h.CreateReset))
		r.Get(route.AdminUserSessions, a.Handle(h.UserSessions))
		r.Delete(route.AdminSession, a.Handle(h.IDAction(ac.RevokeSession)))
		r.Post(route.AdminSigningKeyRotate, a.Handle(h.RotateSigningKey))
	}
}
