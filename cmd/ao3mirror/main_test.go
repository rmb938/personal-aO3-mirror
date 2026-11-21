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

// cli runs the binary's entry point against a fake AO3 with a temp data dir. The API commands
// need serve (c.serve) and fetching needs the daemon (c.daemon), both run in-process.
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

func (c *cli) config() config.Config {
	c.t.Helper()
	cfg, err := config.Load(c.cfg)
	require.NoError(c.t, err)
	return cfg
}

// serve runs the API on the CLI's data dir until the test ends. reveal lets --names work; the
// fixtures are synthetic, so revealing them is safe.
func (c *cli) serve(reveal bool) {
	c.t.Helper()
	cfg := c.config()
	cfg.API.Listen = "127.0.0.1:0" // any free port; config validation wants a real one
	ctx, cancel := context.WithCancel(context.Background())
	s, err := newServer(ctx, cfg, slog.New(slog.DiscardHandler), !reveal)
	if err != nil {
		cancel()
		require.NoError(c.t, err)
	}
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx) }()
	c.t.Cleanup(func() {
		cancel()
		assert.NoError(c.t, <-done, "serve")
	})
}

// daemon starts `ao3mirror run` and waits until it holds the lock. stop ends it and checks its
// exit code.
func (c *cli) daemon() (stop func()) {
	c.t.Helper()
	cfg := c.config()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, cfg, slog.New(slog.DiscardHandler), io.Discard) }()
	require.Eventually(c.t, func() bool { r, _ := daemonRunning(cfg.DataDir); return r }, 10*time.Second, 50*time.Millisecond)
	stopped := false
	stop = func() {
		if !stopped {
			stopped = true
			cancel()
			assert.Equal(c.t, 0, <-done, "daemon exit code")
		}
	}
	c.t.Cleanup(stop)
	return stop
}

func TestRunStatusDebugEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("real pacing (3s floor)")
	}
	c := newCLI(t, "")
	site := fakeao3.NewSite("Fixture Tag", 7000, 2)
	c.srv.SetSite(site)
	c.serve(false)
	code, out, errOut := c.run("source", "add", site.TagPath())
	require.Equal(t, 0, code, "add: %s %s", out, errOut)

	stop := c.daemon()
	require.Eventually(t, func() bool {
		code, out, _ := c.run("status")
		return code == 0 && strings.Contains(out, "fetched: 2,")
	}, 60*time.Second, 200*time.Millisecond, "daemon should fetch both works")
	// A second daemon on the same directory must refuse to start.
	assert.Equal(t, 1, runDaemon(context.Background(), c.config(), slog.New(slog.DiscardHandler), io.Discard),
		"second daemon should be refused by the lock")
	stop()

	code, out, errOut = c.run("status")
	require.Equal(t, 0, code, "status: %s", errOut)
	for _, want := range []string{"== daemon", "not running", "fetched: 2", "ok=2", "work", "done=2", "== sources", "done_backfill", "24h by class: ok="} {
		assert.Contains(t, out, want, "status")
	}
	code, dbg, errOut := c.run("debug", "work", "7000")
	require.Equal(t, 0, code, "debug work: %s", errOut)
	for _, want := range []string{"status:", "ok", "raw versions:", "1 (", "requests:", " work "} {
		assert.Contains(t, dbg, want, "debug work")
	}
	code, dj, errOut := c.run("debug", "job", "1")
	require.Equal(t, 0, code, "debug job: %s", errOut)
	assert.Contains(t, dj, "events:", "debug job")
	assert.Contains(t, dj, "done", "debug job")
	assert.NotContains(t, dj, "null", "debug job events should list only the fields that were set")
	code, _, errOut = c.run("debug", "work", "7999")
	assert.Equal(t, 1, code, "debug of an unknown work")
	assert.Contains(t, errOut, "Not Found", "debug of an unknown work")
	_, jobsOut, _ := c.run("jobs")
	for name, txt := range map[string]string{"status": out, "debug work": dbg, "debug job": dj, "jobs": jobsOut} {
		assert.NotContains(t, strings.ToLower(txt), "fixture", "%s leaked content", name)
	}
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
	c.serve(true)

	// Without a daemon the add is only queued: neither the CLI nor serve talks to AO3.
	code, out, errOut := c.run("source", "add", "/tags/Fixture%20Fandom/works", "--order", "hits_desc")
	require.Equal(t, 0, code, "add: %s", errOut)
	assert.Contains(t, out, "no daemon running", "add output")
	assert.Empty(t, c.srv.Requests(), "source add made a request without the daemon")
	_, out, _ = c.run("source", "add", "/tags/Fixture%20Fandom/works")
	assert.Contains(t, out, "already queued", "second add of a queued source")

	// With a daemon running, add waits for it and prints the result.
	stop := c.daemon()
	code, out, errOut = c.run("source", "add", "/tags/Fixture%20Fandom/works", "--order", "hits_desc", "--wait", "60s")
	require.Equal(t, 0, code, "add: %s", errOut)
	for _, want := range []string{"source 1 added", "state=pending_confirm", "order=hits_desc", "1,234 works", "62 listing pages", "source confirm 1", "page 1: 2 works stored"} {
		assert.Contains(t, out, want, "add output")
	}
	assert.NotContains(t, out, "Fixture", "add output leaked the tag name without --names")
	stop()

	code, _, errOut = c.run("source", "add", "/tags/Fixture%20Fandom")
	assert.Equal(t, 1, code, "duplicate add")
	assert.Contains(t, errOut, "already added as source 1")
	code, _, errOut = c.run("source", "add", "/not/an/ao3/source")
	assert.Equal(t, 1, code, "bad add")
	assert.Contains(t, errOut, "/url:", "a validation error names the invalid member")

	_, out, _ = c.run("source", "list")
	assert.Contains(t, out, "pending_confirm")
	assert.NotContains(t, out, "Fixture", "list must hash names")
	assert.Contains(t, out, "2/0/1,234")
	_, out, _ = c.run("source", "list", "--names")
	assert.Contains(t, out, "Fixture Fandom", "list --names")
	_, out, _ = c.run("source", "show", "1")
	assert.NotContains(t, out, "Fixture", "show must hash the path")
	assert.Contains(t, out, "next page 2, order hits_desc", "show backfill cursor")
	_, out, _ = c.run("source", "show", "1", "--names")
	assert.Contains(t, out, "/tags/Fixture", "show --names")
	// jobs and debug hash targets, paths and reasons unless --names.
	for _, args := range [][]string{{"jobs"}, {"debug", "job", "1"}} {
		code, out, errOut := c.run(args...)
		require.Equal(t, 0, code, "%v: %s", args, errOut)
		assert.Contains(t, out, "h_", "%v must hash targets", args)
		code, out, errOut = c.run(append(args, "--names")...)
		require.Equal(t, 0, code, "%v --names: %s", args, errOut)
		assert.NotContains(t, out, "h_", "%v --names", args)
	}
	_, out, _ = c.run("jobs", "--kind", "work", "--names")
	assert.Contains(t, out, "1001", "jobs --names shows the work id targets")

	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"confirm", "1"}, "confirmed"},
		{[]string{"set", "1", "--poll", "3h", "--priority", "0"}, "poll every 3h0m0s, priority 0"},
		{[]string{"set", "1", "--priority", "2"}, "poll every 3h0m0s, priority 2"},
		{[]string{"refresh", "1"}, "poll due now"},
		{[]string{"pause", "1"}, "paused"},
		{[]string{"resume", "1"}, "resumed"},
		{[]string{"cancel", "1"}, "queued jobs dropped"},
		{[]string{"remove", "1"}, "removed"},
	} {
		code, out, errOut := c.run(append([]string{"source"}, step.args...)...)
		require.Equal(t, 0, code, "%v: %s %s", step.args, out, errOut)
		assert.Contains(t, out, step.want, "%v", step.args)
	}
	code, _, errOut = c.run("source", "show", "1")
	assert.Equal(t, 1, code, "show after remove should fail")
	assert.Contains(t, errOut, "Not Found")
	code, _, _ = c.run("source", "set", "1")
	assert.Equal(t, 2, code, "set without a change is a usage error")
	for _, r := range c.srv.Requests() {
		assert.Contains(t, r.UserAgent, "contact: discord @fixture_user", "%s UA", r.URI)
	}
}

func TestJobsCLI(t *testing.T) {
	c := newCLI(t, "")
	ctx := context.Background()
	st, err := store.Open(ctx, c.config().DataDir, store.Options{})
	require.NoError(t, err)
	for _, j := range []store.NewJob{
		{Kind: "work", Target: "101", Priority: 3, DedupeKey: "work:101"},
		{Kind: "work", Target: "102", Priority: 3, DedupeKey: "work:102"},
		{Kind: "tag_info", Target: "7", Priority: 5, DedupeKey: "tag:7"},
	} {
		_, _, err := st.AutoCommitForTestOnly().EnqueueJob(ctx, j)
		require.NoError(t, err)
	}
	require.NoError(t, st.Close())

	// The API commands need serve.
	code, _, errOut := c.run("jobs")
	assert.Equal(t, 1, code, "jobs without serve")
	assert.Contains(t, errOut, "is the API running?")

	c.serve(false)
	code, out, errOut := c.run("jobs")
	require.Equal(t, 0, code, "jobs: %s", errOut)
	require.Contains(t, out, "ID")
	require.Contains(t, out, "tag_info")
	_, out, _ = c.run("jobs", "--kind", "work", "--state", "queued")
	assert.NotContains(t, out, "tag_info", "kind filter")
	assert.Less(t, strings.Index(out, "\n1 "), strings.Index(out, "\n2 "), "queued jobs in queue order")
	_, out, _ = c.run("jobs", "--limit", "1")
	assert.Contains(t, out, "first 1 of 3", "limit note")
	code, out, _ = c.run("jobs", "--state", "failed")
	assert.Equal(t, 0, code, "empty result should still exit 0")
	assert.Contains(t, out, "no jobs")
	code, _, errOut = c.run("jobs", "--state", "bogus")
	assert.Equal(t, 1, code, "invalid state")
	assert.Contains(t, errOut, "state:", "the invalid parameter is named")
	for _, args := range [][]string{{"source", "list"}, {"jobs"}, {"debug", "job", "1"}} {
		code, _, errOut := c.run(append(args, "--names")...)
		assert.Equal(t, 1, code, "%v --names against a --no-reveal server", args)
		assert.Contains(t, errOut, "Reveal Disabled", "%v --names", args)
	}
	code, _, _ = c.run("debug", "job", "1", "extra")
	assert.Equal(t, 2, code, "debug with an extra argument")
	assert.Empty(t, c.srv.Requests(), "jobs must make no requests")
}

func TestFetchWorkAndReparse(t *testing.T) {
	if testing.Short() {
		t.Skip("real pacing (3s floor)")
	}
	c := newCLI(t, "")
	c.srv.SetSite(fakeao3.NewSite("Fixture Tag", 7100, 2))

	// fetch-work and reparse are direct commands: they need no serve.
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
	code, _, _ = c.run("fetch-work", "/tags/Fixture%20Tag/works")
	require.Equal(t, 2, code, "a non-work URL is a usage error")

	before := len(c.srv.Requests())
	code, out, errOut = c.run("reparse", "--all")
	require.Equal(t, 0, code, "reparse: %s", errOut)
	require.Contains(t, out, "reparsed 1")
	require.Len(t, c.srv.Requests(), before, "reparse must make no requests")
	code, out, _ = c.run("reparse")
	require.Equal(t, 0, code)
	require.Contains(t, out, "skipped 1", "second reparse should skip current-version works")

	c.serve(false)
	_, dbg, _ := c.run("debug", "work", "7999")
	require.Contains(t, dbg, "deleted")
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
