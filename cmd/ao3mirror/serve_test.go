package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

func serveConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Contact.Discord = "@fixture_user"
	cfg.DataDir = t.TempDir()
	require.NoError(t, cfg.Validate())
	cfg.API.Listen = "127.0.0.1:0" // any free port; config validation wants a real one
	return cfg
}

func unixClient(path string) *http.Client {
	var d net.Dialer
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialContext(ctx, "unix", path) },
	}}
}

// get returns the status, headers and decoded JSON body of a GET.
func get(t *testing.T, c *http.Client, url string) (int, http.Header, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return resp.StatusCode, resp.Header, body
}

func TestServeListenersAndShutdown(t *testing.T) {
	cfg := serveConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := newServer(ctx, cfg, slog.New(slog.DiscardHandler), true)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx) }()

	base := "http://" + s.tcp.Addr().String()
	tcp := &http.Client{Timeout: 5 * time.Second}
	status, hdr, _ := get(t, tcp, base+"/api/v1/healthz")
	assert.Equal(t, http.StatusOK, status, "public healthz")
	assert.Equal(t, "hashed", hdr.Get("X-Fanwork"))

	// --no-reveal: the reveal param is refused, not honoured.
	status, _, body := get(t, tcp, base+"/api/v1/healthz?"+runtime.RevealParam+"=1")
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, string(dto.TypeRevealDisabled), body["type"])

	// The store is open: a store-backed route answers on a fresh data dir.
	status, _, body = get(t, tcp, base+"/api/v1/sources")
	assert.Equal(t, http.StatusOK, status, "public sources")
	assert.Equal(t, map[string]any{"items": []any{}, "page": 1.0, "per_page": 20.0, "total": 0.0}, body)

	sockPath := cfg.Path(cfg.API.AdminSocket)
	status, _, _ = get(t, unixClient(sockPath), "http://admin/api/v1/version")
	assert.Equal(t, http.StatusOK, status, "admin version")

	st, err := os.Lstat(sockPath)
	require.NoError(t, err)
	assert.Equal(t, fs.ModeSocket, st.Mode().Type())
	assert.Equal(t, fs.FileMode(0o600), st.Mode().Perm(), "socket mode")
	st, err = os.Stat(filepath.Dir(sockPath))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), st.Mode().Perm(), "socket dir mode")

	// A second server must not take over a live socket.
	_, err = newServer(ctx, cfg, slog.New(slog.DiscardHandler), true)
	require.ErrorContains(t, err, "in use")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("serve did not shut down")
	}
	_, err = os.Lstat(sockPath)
	assert.ErrorIs(t, err, fs.ErrNotExist, "socket removed on shutdown")
}

func TestServeListenerFailureStopsBoth(t *testing.T) {
	cfg := serveConfig(t)
	s, err := newServer(t.Context(), cfg, slog.New(slog.DiscardHandler), true)
	require.NoError(t, err)
	require.NoError(t, s.tcp.Close()) // public Serve fails at once
	require.Error(t, s.serve(t.Context()))
	_, err = os.Lstat(cfg.Path(cfg.API.AdminSocket))
	assert.ErrorIs(t, err, fs.ErrNotExist, "admin socket closed too")
}

func TestAdminSocketChecks(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "api")
	path := filepath.Join(dir, "admin.sock")

	// A stale socket (crashed server) is replaced.
	require.NoError(t, os.Mkdir(dir, 0o700))
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "unix", path)
	require.NoError(t, err)
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, l.Close())
	l, err = listenAdminSocket(ctx, path)
	require.NoError(t, err, "stale socket")
	require.NoError(t, l.Close())

	// Anything else at the path is left alone.
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	_, err = listenAdminSocket(ctx, path)
	require.ErrorContains(t, err, "not a socket")
	_, err = os.Stat(path)
	require.NoError(t, err, "regular file kept")
	require.NoError(t, os.Remove(path))

	// A directory others can reach is refused.
	require.NoError(t, os.Chmod(dir, 0o750)) //nolint:gosec // deliberately unsafe mode under test
	_, err = listenAdminSocket(ctx, path)
	require.ErrorContains(t, err, "chmod 700")

	_, err = listenAdminSocket(ctx, filepath.Join(dir, strings.Repeat("x", maxSocketPath)))
	require.ErrorContains(t, err, "unix socket limit")
}

func TestServeArgs(t *testing.T) {
	c := newCLI(t, "")
	for _, args := range [][]string{{"serve", "extra"}, {"serve", "--bogus"}} {
		code, _, errOut := c.run(args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Contains(t, errOut, "usage: ao3mirror serve", "%v", args)
	}
}
