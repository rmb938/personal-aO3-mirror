package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
)

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
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &cli{t: t, cfg: cfg, srv: srv}
}

func (c *cli) run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(append([]string{"--config", c.cfg, "--log-level", "error"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSourceCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("real pacing (3s floor)")
	}
	c := newCLI(t, "sources: {auto_confirm_max: 100}\n")
	c.srv.Handle("/tags/Fixture%20Fandom/works", fakeao3.Reply{Body: fixture(t, "listing_tag.html")})

	code, out, errOut := c.run("source", "add", "/tags/Fixture%20Fandom/works", "--order", "hits_desc")
	if code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	for _, want := range []string{"source 1 added", "state=pending_confirm", "order=hits_desc", "1,234 works", "62 listing pages", "source confirm 1", "page 1: 2 works stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("add output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Fixture") {
		t.Errorf("add output leaked the tag name without --names:\n%s", out)
	}
	if !strings.Contains(out, httpx.Hash("Fixture Fandom")) {
		t.Errorf("add output should show the hashed target:\n%s", out)
	}

	if code, _, errOut := c.run("source", "add", "/tags/Fixture%20Fandom"); code != 1 || !strings.Contains(errOut, "already added as source 1") {
		t.Errorf("duplicate: %d %s", code, errOut)
	}

	_, out, _ = c.run("source", "list")
	if !strings.Contains(out, "pending_confirm") || strings.Contains(out, "Fixture") || !strings.Contains(out, "2/0/1,234") {
		t.Errorf("list:\n%s", out)
	}
	_, out, _ = c.run("source", "list", "--names")
	if !strings.Contains(out, "Fixture Fandom") {
		t.Errorf("list --names:\n%s", out)
	}
	_, out, _ = c.run("source", "show", "1")
	if strings.Contains(out, "Fixture") || !strings.Contains(out, "/tags/h") {
		t.Errorf("show must hash the path:\n%s", out)
	}

	for _, step := range [][]string{{"confirm", "1"}, {"pause", "1"}, {"resume", "1"}, {"cancel", "1"}, {"remove", "1"}} {
		if code, out, errOut := c.run(append([]string{"source"}, step...)...); code != 0 {
			t.Fatalf("%v: %d %s %s", step, code, out, errOut)
		}
	}
	if code, _, _ := c.run("source", "show", "1"); code != 1 {
		t.Error("show after remove should fail")
	}
	for _, r := range c.srv.Requests() {
		if !strings.Contains(r.UserAgent, "contact: discord @fixture_user") {
			t.Errorf("%s sent UA %q", r.URI, r.UserAgent)
		}
	}
}

func TestBaseURLLoopbackOnly(t *testing.T) {
	t.Setenv("AO3MIRROR_BASE_URL", "https://example.com")
	if _, err := baseURL(); err == nil {
		t.Fatal("non-loopback base URL must be rejected")
	}
	t.Setenv("AO3MIRROR_BASE_URL", "http://127.0.0.1:8080")
	if v, err := baseURL(); err != nil || v != "http://127.0.0.1:8080" {
		t.Fatal(v, err)
	}
	t.Setenv("AO3MIRROR_BASE_URL", "")
	if v, _ := baseURL(); v != httpx.DefaultBaseURL {
		t.Fatal(v)
	}
}
