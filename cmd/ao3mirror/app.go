package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/scheduler"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// app bundles the long-lived pieces commands need.
type app struct {
	cfg    config.Config
	log    *slog.Logger
	store  *store.Store
	client *httpx.Client
	ctl    *controllers.Controllers
}

func openApp(ctx context.Context, cfg config.Config, log *slog.Logger) (*app, error) {
	st, err := store.Open(ctx, cfg.DataDir, store.Options{Logger: log})
	if err != nil {
		return nil, err
	}
	a := &app{cfg: cfg, log: log, store: st}
	base, err := baseURL()
	if err != nil {
		st.Close()
		return nil, err
	}
	a.client, err = httpx.New(httpx.Options{
		BaseURL:     base,
		UserAgent:   cfg.UserAgent(version),
		Interval:    cfg.Rate.Interval,
		Jitter:      cfg.Rate.Jitter,
		DailyBudget: cfg.Rate.DailyBudget,
		Logger:      log,
		OnRequest: func(r httpx.RequestRecord) {
			// Content-free request log for `status` and debugging.
			if err := st.InsertRequest(context.Background(), store.RequestRow{
				Time: r.Time, ReqID: r.ReqID, Kind: string(r.Kind), Path: r.SafePath, Status: r.Status,
				Class: string(r.Class), Reason: r.Reason, Bytes: r.Bytes, Duration: r.Duration, Waited: r.Waited, Hop: r.Hop, JobID: r.JobID,
			}); err != nil {
				log.Warn("request log insert failed", "err", err.Error())
			}
		},
	})
	if err != nil {
		st.Close()
		return nil, err
	}
	a.ctl = newControllers(cfg, log, st, a.client)
	return a, nil
}

func (a *app) Close() { a.store.Close() }

// newControllers builds the business layer. client is nil for offline commands (reparse): no
// HTTP client is ever built for them, and any AO3 fetch fails with controllers.ErrOffline.
func newControllers(cfg config.Config, log *slog.Logger, st *store.Store, client *httpx.Client) *controllers.Controllers {
	o := controllers.Options{
		Store: st, Logger: log, Now: time.Now,
		DataDir: cfg.DataDir, KeepVersions: cfg.Content.KeepVersions, QueueImages: cfg.Images.Enabled,
		Sources: cfg.Sources, Interval: cfg.Rate.Interval,
		TagInfoTypes: cfg.Tags.InfoTypes, TagInfoTTL: cfg.Tags.InfoTTL,
		RescanInterval: cfg.Sources.FullRescanInterval,
		SeriesInfo:     cfg.Series.Info, SeriesInfoTTL: cfg.Series.InfoTTL,
	}
	if client != nil { // a typed nil would defeat the controllers' offline check
		o.Client = client
	}
	return controllers.New(o)
}

func (a *app) scheduler() *scheduler.Scheduler {
	return scheduler.New(scheduler.Options{Store: a.store, Controllers: a.ctl, Pacer: a.client, Logger: a.log})
}

// baseURL returns the AO3 base URL. AO3MIRROR_BASE_URL may point at a local fake AO3
// (tests, `dev fake-server`); only loopback hosts are accepted.
func baseURL() (string, error) {
	v := os.Getenv("AO3MIRROR_BASE_URL")
	if v == "" {
		return httpx.DefaultBaseURL, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("AO3MIRROR_BASE_URL: %w", err)
	}
	if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("AO3MIRROR_BASE_URL must be a loopback address (fake AO3 only), got %q", u.Hostname())
	}
	return v, nil
}
