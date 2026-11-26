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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/dirs/dirstest"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
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

// testData opens cfg's data dir as main does, closed when the test ends.
func testData(t *testing.T, cfg config.Config) *dataDirs {
	t.Helper()
	d, err := openDataDirs(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })
	return d
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
	s, err := newServer(ctx, cfg, testData(t, cfg), slog.New(slog.DiscardHandler), true)
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

	// Every public route but healthz and the auth entry points needs an access token.
	status, hdr, body = get(t, tcp, base+"/api/v1/sources")
	assert.Equal(t, http.StatusUnauthorized, status, "public sources without a token")
	assert.Equal(t, string(dto.TypeUnauthorized), body["type"])
	assert.Equal(t, runtime.WWWAuthenticate, hdr.Get("WWW-Authenticate"))

	// The admin socket needs none, and the store is open: a store-backed route answers on a
	// fresh data dir.
	sockPath := cfg.Path(cfg.API.AdminSocket)
	status, _, _ = get(t, unixClient(sockPath), "http://admin/api/v1/version")
	assert.Equal(t, http.StatusOK, status, "admin version")
	status, _, body = get(t, unixClient(sockPath), "http://admin/api/v1/sources")
	assert.Equal(t, http.StatusOK, status, "admin sources")
	assert.Equal(t, map[string]any{"items": []any{}, "page": 1.0, "per_page": 20.0, "total": 0.0}, body)

	st, err := os.Lstat(sockPath)
	require.NoError(t, err)
	assert.Equal(t, fs.ModeSocket, st.Mode().Type())
	assert.Equal(t, fs.FileMode(0o600), st.Mode().Perm(), "socket mode")
	st, err = os.Stat(filepath.Dir(sockPath))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), st.Mode().Perm(), "socket dir mode")
	for _, name := range []string{store.UsersFile, store.KeysFile} {
		st, err := os.Stat(filepath.Join(filepath.Dir(sockPath), name))
		require.NoError(t, err, name)
		assert.Equal(t, fs.FileMode(0o600), st.Mode().Perm(), name)
	}

	// A second server must not take over a live socket.
	_, err = newServer(ctx, cfg, testData(t, cfg), slog.New(slog.DiscardHandler), true)
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
	s, err := newServer(t.Context(), cfg, testData(t, cfg), slog.New(slog.DiscardHandler), true)
	require.NoError(t, err)
	require.NoError(t, s.tcp.Close()) // public Serve fails at once
	require.Error(t, s.serve(t.Context()))
	_, err = os.Lstat(cfg.Path(cfg.API.AdminSocket))
	assert.ErrorIs(t, err, fs.ErrNotExist, "admin socket closed too")
}

// TestOpenAuth checks the accounts are opened from config: invite links start with
// api.public_url, keys.db and users.db survive a restart (keys.db's first master key is made
// once), and a non-private directory is refused.
func TestOpenAuth(t *testing.T) {
	cfg := serveConfig(t)
	cfg.API.AllowedHosts = append(cfg.API.AllowedHosts, "mirror.example.com")
	cfg.API.PublicURL = "https://mirror.example.com"
	cfg.Auth.InviteTTL = 2 * time.Hour
	logs := &lockedBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	const created = `"event":"master_key_created","key_id":1`
	data := testData(t, cfg)

	acc, err := openAuth(t.Context(), cfg, data, log)
	require.NoError(t, err)
	assert.Equal(t, 1, logs.count(created), "first start creates the master key")
	before := time.Now()
	link, err := acc.auth.CreateInvite(t.Context(), 0)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(link.URL, "https://mirror.example.com/signup#token="), "public_url is the link base")
	assert.WithinDuration(t, before.Add(2*time.Hour), link.ExpiresAt, time.Minute, "auth.invite_ttl is the default")
	require.NoError(t, acc.Close())

	// A restart reads the same users.db and keys.db.
	acc, err = openAuth(t.Context(), cfg, data, log)
	require.NoError(t, err)
	assert.Equal(t, 1, logs.count(created), "a restart reuses the master key")
	invites, err := acc.auth.ListInvites(t.Context())
	require.NoError(t, err)
	require.Len(t, invites, 1)
	assert.Equal(t, link.ID, invites[0].ID)
	require.NoError(t, acc.Close())

	dir := filepath.Dir(cfg.Path(cfg.API.AdminSocket))
	_, err = os.Stat(filepath.Join(dir, "auth.key"))
	require.ErrorIs(t, err, fs.ErrNotExist, "auth.key is gone")

	require.NoError(t, os.Chmod(dir, 0o750)) //nolint:gosec // deliberately unsafe mode under test
	_, err = openAuth(t.Context(), cfg, data, log)
	require.ErrorContains(t, err, "chmod 700")
	require.NoError(t, os.Chmod(dir, 0o700)) //nolint:gosec // G302: a directory; 0700 is the private mode

	// So is a backup directory others can reach.
	require.NoError(t, os.Chmod(cfg.Path(cfg.API.BackupDir), 0o750)) //nolint:gosec // deliberately unsafe mode under test
	_, err = openAuth(t.Context(), cfg, data, log)
	require.ErrorContains(t, err, "api.backup_dir")
}

// lockedBuffer is a log sink shared with a goroutine under test.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) count(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.b.String(), s)
}

// TestKeyChecks checks serve's key check loop: a key already due rotates at once, a key that
// becomes due rotates on a later tick, a retired master key is deleted once its retention has
// passed, and the loop stops with its context.
func TestKeyChecks(t *testing.T) {
	dir := dirstest.At(t, filepath.Join(t.TempDir(), "api"), 0o700)
	var offset atomic.Int64
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return t0.Add(time.Duration(offset.Load())) }
	users, err := store.OpenUsers(t.Context(), dir, store.Options{Now: now})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, users.Close()) })
	keys, err := store.OpenKeys(t.Context(), dir, store.Options{Now: now})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keys.Close()) })
	logs := &lockedBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	mk, err := controllers.NewMasterKeys(t.Context(), controllers.MasterKeysOptions{Keys: keys, Users: users,
		Logger: log, Now: now, Rotation: 48 * time.Hour})
	require.NoError(t, err)
	keyring := mk.Keyring()
	hasher, err := auth.NewHasher(keyring, auth.HasherOptions{})
	require.NoError(t, err)
	ctl, err := controllers.NewAuth(t.Context(), controllers.AuthOptions{Users: users, MasterKeys: mk,
		Hasher: hasher, Logger: log, Now: now, PublicURL: "https://mirror.example.com",
		SigningKeyRotation: 24 * time.Hour})
	require.NoError(t, err)
	acc := accounts{users: users, keys: keys, master: mk, auth: ctl}
	const (
		signingRotated = `"event":"signing_key_rotated","reason":"scheduled"`
		masterRotated  = `"event":"master_key_rotated","reason":"scheduled"`
		masterDeleted  = `"event":"master_key_deleted"`
	)

	offset.Store(int64(25 * time.Hour)) // the server was down past the signing key's due date
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runKeyChecks(ctx, log, 10*time.Millisecond, keyChecks(acc))
	}()
	require.Eventually(t, func() bool { return logs.count(signingRotated) == 1 }, 5*time.Second, 5*time.Millisecond,
		"rotated at startup")
	time.Sleep(50 * time.Millisecond) // a few ticks with nothing due
	assert.Equal(t, 1, logs.count(signingRotated), "not due again yet")
	assert.Zero(t, logs.count(masterRotated), "master key not due yet")

	offset.Store(int64(49 * time.Hour))
	require.Eventually(t, func() bool { return logs.count(signingRotated) == 2 && logs.count(masterRotated) == 1 },
		5*time.Second, 5*time.Millisecond, "rotated on a tick")
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, logs.count(masterDeleted), "retired key kept")

	offset.Store(int64(49*time.Hour + 7*24*time.Hour)) // the retired key's retention has passed
	require.Eventually(t, func() bool { return logs.count(masterDeleted) == 1 }, 5*time.Second, 5*time.Millisecond,
		"retired key deleted on a tick")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("key check loop did not stop")
	}
	assert.Zero(t, logs.count("check failed"))
}

// TestAuthBoundsMatchController keeps config's invite TTL bounds in step with the controller's.
func TestAuthBoundsMatchController(t *testing.T) {
	assert.Equal(t, controllers.MinInviteTTL, config.MinInviteTTL)
	assert.Equal(t, controllers.MaxInviteTTL, config.MaxInviteTTL)
}

func TestAdminSocketChecks(t *testing.T) {
	ctx := t.Context()
	base := t.TempDir()
	data := dirstest.At(t, base, 0o750)
	const rel = "api/admin.sock"
	dir := filepath.Join(base, "api")
	path := filepath.Join(base, rel)

	// A missing directory is created private; a stale socket (crashed server) is replaced.
	l, err := listenAdminSocket(ctx, data, rel)
	require.NoError(t, err, "new directory")
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, l.Close())
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	l, err = listenAdminSocket(ctx, data, rel)
	require.NoError(t, err, "stale socket")
	require.NoError(t, l.Close())

	// Anything else at the path is left alone.
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	_, err = listenAdminSocket(ctx, data, rel)
	require.ErrorContains(t, err, "not a socket")
	_, err = os.Stat(path)
	require.NoError(t, err, "regular file kept")
	require.NoError(t, os.Remove(path))

	// A directory others can reach is refused.
	require.NoError(t, os.Chmod(dir, 0o750)) //nolint:gosec // deliberately unsafe mode under test
	_, err = listenAdminSocket(ctx, data, rel)
	require.ErrorContains(t, err, "chmod 700")
	require.NoError(t, os.Chmod(dir, 0o700)) //nolint:gosec // G302: a directory; 0700 is the private mode

	_, err = listenAdminSocket(ctx, data, "api/"+strings.Repeat("x", maxSocketPath))
	require.ErrorContains(t, err, "unix socket limit")

	// The socket stays inside the data dir.
	_, err = listenAdminSocket(ctx, data, "../escape/admin.sock")
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(filepath.Dir(base), "escape"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestServeArgs(t *testing.T) {
	c := newCLI(t, "")
	for _, args := range [][]string{{"serve", "extra"}, {"serve", "--bogus"}} {
		code, _, errOut := c.run(args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Contains(t, errOut, "usage: ao3mirror serve", "%v", args)
	}
}
