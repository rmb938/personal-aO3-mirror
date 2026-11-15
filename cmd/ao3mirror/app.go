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
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/scheduler"
	"github.com/rmb938/personal-aO3-mirror/internal/sources"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// app bundles the long-lived pieces commands need.
type app struct {
	cfg    config.Config
	log    *slog.Logger
	store  *store.Store
	client *httpx.Client
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
	return a, nil
}

func (a *app) Close() { a.store.Close() }

func (a *app) schedulerOptions() scheduler.Options {
	return scheduler.Options{
		Store: a.store, Client: a.client, Logger: a.log,
		DataDir: a.cfg.DataDir, KeepVersions: a.cfg.Content.KeepVersions,
		TagInfoTypes: a.cfg.Tags.InfoTypes, TagInfoTTL: a.cfg.Tags.InfoTTL,
		RescanInterval: a.cfg.Sources.FullRescanInterval,
		QueueImages:    a.cfg.Images.Enabled,
	}
}

// appCfg is what offline commands (reparse) need: no HTTP client is ever built for them.
type appCfg struct {
	dataDir string
	log     *slog.Logger
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

func (a *app) sources() *sources.Service {
	return sources.New(sources.Options{
		Store: a.store, Client: a.client, Config: a.cfg.Sources,
		Interval: a.cfg.Rate.Interval, Logger: a.log, Now: time.Now,
	})
}
