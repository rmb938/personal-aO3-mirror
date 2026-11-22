package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// apiTimeout bounds one CLI call to the admin socket, from dial to the end of the response.
const apiTimeout = time.Minute

// imageDir is the image worker's directory under data_dir, which `serve` reads images from.
const imageDir = "images"

// app bundles what the API commands (source, jobs, status, debug) need: the config and a
// client for the admin socket of a running `ao3mirror serve`. They never open the store.
type app struct {
	cfg    config.Config
	client *apiclient.Client
}

func openApp(cfg config.Config) (*app, error) {
	sock := cfg.Path(cfg.API.AdminSocket)
	var d net.Dialer
	hc := &http.Client{
		Timeout: apiTimeout,
		Transport: &http.Transport{
			// Every request goes to the admin socket, whatever the URL's host.
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", sock)
			},
			MaxIdleConnsPerHost: 1,
		},
	}
	c, err := apiclient.NewClient("http://admin.sock", apiclient.WithClient(hc))
	if err != nil {
		return nil, err
	}
	return &app{cfg: cfg, client: c}, nil
}

// newControllers builds the business layer. client is nil for offline commands (reparse, serve):
// no HTTP client is ever built for them, and any AO3 fetch fails with controllers.ErrOffline.
func newControllers(cfg config.Config, log *slog.Logger, st *store.Store, client *httpx.Client) *controllers.Controllers {
	return controllers.New(controllerOptions(cfg, log, st, client))
}

// controllerOptions are newControllers' options, for a caller that adds to them (serve's Auth).
func controllerOptions(cfg config.Config, log *slog.Logger, st *store.Store, client *httpx.Client) controllers.Options {
	o := controllers.Options{
		Store: st, Logger: log, Now: time.Now,
		DataDir: cfg.DataDir, KeepVersions: cfg.Content.KeepVersions, QueueImages: cfg.Images.Enabled,
		Sources: cfg.Sources, Interval: cfg.Rate.Interval, ImageDir: cfg.Path(imageDir),
		TagInfoTypes: cfg.Tags.InfoTypes, TagInfoTTL: cfg.Tags.InfoTTL,
		SeriesInfo: cfg.Series.Info, SeriesInfoTTL: cfg.Series.InfoTTL,
	}
	if client != nil { // a typed nil would defeat the controllers' offline check
		o.Client = client
	}
	return o
}

// newAO3Client builds the one AO3 client of a fetching process (`run`, `fetch-work --now`). Every
// request it makes is logged, content-free, to the requests table for `status` and `debug`.
func newAO3Client(cfg config.Config, log *slog.Logger, st *store.Store) (*httpx.Client, error) {
	base, err := baseURL()
	if err != nil {
		return nil, err
	}
	return httpx.New(httpx.Options{
		BaseURL:     base,
		UserAgent:   cfg.UserAgent(version),
		Interval:    cfg.Rate.Interval,
		Jitter:      cfg.Rate.Jitter,
		DailyBudget: cfg.Rate.DailyBudget,
		Logger:      log,
		OnRequest: func(r httpx.RequestRecord) {
			// Its own one-row transaction: the request ended outside any.
			row := store.RequestRow{
				Time: r.Time, ReqID: r.ReqID, Kind: string(r.Kind), Path: r.SafePath, Status: r.Status,
				Class: string(r.Class), Reason: r.Reason, Bytes: r.Bytes, Duration: r.Duration, Waited: r.Waited, Hop: r.Hop, JobID: r.JobID,
			}
			if err := st.InTx(context.Background(), func(ctx context.Context, tx *store.Tx) error {
				return tx.InsertRequest(ctx, row)
			}); err != nil {
				log.Warn("request log insert failed", "err", err.Error())
			}
		},
	})
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
