package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestRunStatusDebugEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("real pacing (3s floor)")
	}
	c := newCLI(t, "")
	site := fakeao3.NewSite("Fixture Tag", 7000, 2)
	c.srv.SetSite(site)
	code, out, errOut := c.run("source", "add", site.TagPath())
	require.Equal(t, 0, code, "add: %s %s", out, errOut)

	cfg, err := config.Load(c.cfg)
	require.NoError(t, err)
	a, err := openApp(context.Background(), cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(a.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- runDaemon(ctx, a, io.Discard) }()
	require.Eventually(t, func() bool {
		rep, err := a.ctl.Status(context.Background())
		return err == nil && rep.Counts.WorksFetched == 2
	}, 60*time.Second, 200*time.Millisecond, "daemon should fetch both works")
	// A second daemon on the same directory must refuse to start.
	assert.Equal(t, 1, runDaemon(context.Background(), a, io.Discard), "second daemon should be refused by the lock")
	cancel()
	require.Equal(t, 0, <-done, "daemon exit code")

	_, out, _ = c.run("status")
	for _, want := range []string{"== daemon", "fetched: 2", "ok=2", "work", "done=2", "== sources", "done_backfill"} {
		assert.Contains(t, out, want, "status")
	}
	_, dbg, _ := c.run("debug", "work", "7000")
	for _, want := range []string{"status:", "ok", "raw versions:", "1 (", "requests:", "/works/7000?view_full_work=true"} {
		assert.Contains(t, dbg, want, "debug work")
	}
	_, dj, _ := c.run("debug", "job", "1")
	assert.Contains(t, dj, "events:", "debug job")
	assert.Contains(t, dj, "done", "debug job")
	for name, txt := range map[string]string{"status": out, "debug work": dbg, "debug job": dj} {
		assert.NotContains(t, strings.ToLower(txt), "fixture", "%s leaked content", name)
	}
}

// cli runs the binary's entry point against a fake AO3 with a temp data dir.
type cli struct {
	t   *testing.T
	cfg string
	srv *fakeao3.Server
}

func newCLI(t *testing.T, extraYAML string) *cli {
	t.Helper()
	srv := fakeao3.New()
	t.Cleanup(srv.Close)
	t.Setenv("AO3MIRROR_BASE_URL", srv.URL)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg.yaml")
	body := "data_dir: " + filepath.Join(dir, "d") + "\ncontact: {discord: \"@fixture_user\"}\nrate: {interval: 3s, jitter: 0}\n" + extraYAML
	require.NoError(t, os.WriteFile(cfg, []byte(body), 0o600))
	return &cli{t: t, cfg: cfg, srv: srv}
}

func (c *cli) run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(append([]string{"--config", c.cfg, "--log-level", "error"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	require.NoError(t, err)
	return string(b)
}

func TestSourceCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("real pacing (3s floor)")
	}
	c := newCLI(t, "sources: {auto_confirm_max: 100}\n")
	c.srv.Handle("/tags/Fixture%20Fandom/works", fakeao3.Reply{Body: fixture(t, "listing_tag.html")})

	code, out, errOut := c.run("source", "add", "/tags/Fixture%20Fandom/works", "--order", "hits_desc")
	require.Equal(t, 0, code, "add: %s", errOut)
	for _, want := range []string{"source 1 added", "state=pending_confirm", "order=hits_desc", "1,234 works", "62 listing pages", "source confirm 1", "page 1: 2 works stored"} {
		assert.Contains(t, out, want, "add output")
	}
	assert.NotContains(t, out, "Fixture", "add output leaked the tag name without --names")
	assert.Contains(t, out, httpx.Hash("Fixture Fandom"), "add output should show the hashed target")

	code, _, errOut = c.run("source", "add", "/tags/Fixture%20Fandom")
	assert.Equal(t, 1, code, "duplicate add")
	assert.Contains(t, errOut, "already added as source 1")

	_, out, _ = c.run("source", "list")
	assert.Contains(t, out, "pending_confirm")
	assert.NotContains(t, out, "Fixture", "list must hash names")
	assert.Contains(t, out, "2/0/1,234")
	_, out, _ = c.run("source", "list", "--names")
	assert.Contains(t, out, "Fixture Fandom", "list --names")
	_, out, _ = c.run("source", "show", "1")
	assert.NotContains(t, out, "Fixture", "show must hash the path")
	assert.Contains(t, out, "/tags/h", "show must hash the path")

	for _, step := range [][]string{{"confirm", "1"}, {"pause", "1"}, {"resume", "1"}, {"cancel", "1"}, {"remove", "1"}} {
		code, out, errOut := c.run(append([]string{"source"}, step...)...)
		require.Equal(t, 0, code, "%v: %s %s", step, out, errOut)
	}
	code, _, _ = c.run("source", "show", "1")
	assert.Equal(t, 1, code, "show after remove should fail")
	for _, r := range c.srv.Requests() {
		assert.Contains(t, r.UserAgent, "contact: discord @fixture_user", "%s UA", r.URI)
	}
}

func TestJobsCLI(t *testing.T) {
	c := newCLI(t, "")
	cfg, err := config.Load(c.cfg)
	require.NoError(t, err)
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DataDir, store.Options{})
	require.NoError(t, err)
	for _, j := range []store.Job{
		{Kind: "work", Target: "101", Priority: 3, DedupeKey: "work:101"},
		{Kind: "work", Target: "102", Priority: 3, DedupeKey: "work:102"},
		{Kind: "tag_info", Target: "7", Priority: 5, DedupeKey: "tag:7"},
	} {
		_, _, err := st.AutoCommitForTestOnly().EnqueueJob(ctx, j)
		require.NoError(t, err)
	}
	require.NoError(t, st.Close())

	code, out, errOut := c.run("jobs")
	require.Equal(t, 0, code, "jobs: %s", errOut)
	require.Contains(t, out, "ID")
	require.Contains(t, out, "tag_info")
	require.Contains(t, out, "102")
	_, out, _ = c.run("jobs", "--kind", "work", "--state", "queued")
	assert.NotContains(t, out, "tag_info", "kind filter")
	assert.Less(t, strings.Index(out, "101"), strings.Index(out, "102"), "queued jobs in queue order")
	_, out, _ = c.run("jobs", "--limit", "1")
	assert.Contains(t, out, "first 1", "limit note")
	code, _, _ = c.run("jobs", "--state", "failed")
	assert.Equal(t, 0, code, "empty result should still exit 0")
	assert.Empty(t, c.srv.Requests(), "jobs must make no requests")
}

func TestBaseURLLoopbackOnly(t *testing.T) {
	t.Setenv("AO3MIRROR_BASE_URL", "https://example.com")
	_, err := baseURL()
	require.Error(t, err, "non-loopback base URL must be rejected")
	t.Setenv("AO3MIRROR_BASE_URL", "http://127.0.0.1:8080")
	v, err := baseURL()
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080", v)
	t.Setenv("AO3MIRROR_BASE_URL", "")
	v, err = baseURL()
	require.NoError(t, err)
	require.Equal(t, httpx.DefaultBaseURL, v)
}

func TestFetchWorkAndReparse(t *testing.T) {
	if testing.Short() {
		t.Skip("real pacing (3s floor)")
	}
	c := newCLI(t, "")
	c.srv.SetSite(fakeao3.NewSite("Fixture Tag", 7100, 2))

	code, out, errOut := c.run("fetch-work", "/works/7100/chapters/5")
	require.Equal(t, 0, code, "queue: %s", errOut)
	require.Contains(t, out, "work 7100 queued as job 1")
	code, out, _ = c.run("fetch-work", "7100")
	require.Equal(t, 0, code)
	require.Contains(t, out, "already queued", "requeue")
	code, out, errOut = c.run("fetch-work", "--now", "7100")
	require.Equal(t, 0, code, "--now: %s", errOut)
	require.Contains(t, out, "job 1 done")
	code, out, _ = c.run("fetch-work", "7999", "--now")
	require.Equal(t, 0, code)
	require.Contains(t, out, "done", "missing work should finish (marked deleted)")
	_, dbg, _ := c.run("debug", "work", "7999")
	require.Contains(t, dbg, "deleted")

	before := len(c.srv.Requests())
	code, out, errOut = c.run("reparse", "--all")
	require.Equal(t, 0, code, "reparse: %s", errOut)
	require.Contains(t, out, "reparsed 1")
	require.Len(t, c.srv.Requests(), before, "reparse must make no requests")
	code, out, _ = c.run("reparse")
	require.Equal(t, 0, code)
	require.Contains(t, out, "skipped 1", "second reparse should skip current-version works")
}
