package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// app bundles the long-lived pieces commands need.
type app struct {
	cfg       config.Config
	log       *slog.Logger
	apiClient *apiclient.Client
}

func openApp(cfg config.Config, log *slog.Logger) (*app, error) {
	a := &app{cfg: cfg, log: log}
	clientOpt := apiclient.WithClient(&http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   1,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,

			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				adminSocket := path.Join(a.cfg.DataDir, "api", "admin.sock")
				return d.DialContext(ctx, "unix", adminSocket)
			},
		},
	})

	var err error
	a.apiClient, err = apiclient.NewClient("http://unix", clientOpt)
	if err != nil {
		return nil, err
	}

	return a, nil
}

// newControllers builds the business layer. client is nil for offline commands (reparse): no
// HTTP client is ever built for them, and any AO3 fetch fails with controllers.ErrOffline.
func newControllers(cfg config.Config, log *slog.Logger, st *store.Store, client *httpx.Client) *controllers.Controllers {
	o := controllers.Options{
		Store: st, Logger: log, Now: time.Now,
		DataDir: cfg.DataDir, KeepVersions: cfg.Content.KeepVersions, QueueImages: cfg.Images.Enabled,
		Sources: cfg.Sources, Interval: cfg.Rate.Interval,
		TagInfoTypes: cfg.Tags.InfoTypes, TagInfoTTL: cfg.Tags.InfoTTL,
		SeriesInfo: cfg.Series.Info, SeriesInfoTTL: cfg.Series.InfoTTL,
	}
	if client != nil { // a typed nil would defeat the controllers' offline check
		o.Client = client
	}
	return controllers.New(o)
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
