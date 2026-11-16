// Command skeletonize turns an AO3 page into a structure-only skeleton that is safe to share with
// Claude for parser debugging: no text, no names, no attribute values except structure.
//
// Run it YOURSELF (not Claude). Give it an AO3 URL (fetched politely through internal/httpx with
// your configured User-Agent, robots.txt check and pacing) or a saved file:
//
//	go run ./cmd/skeletonize --config ao3mirror.local.yaml \
//	    -o testdata/skeletons/work-chaptered.skel.html https://archiveofourown.org/works/123
//
// Work URLs (/works/<id>) automatically get view_full_work=true (the client sends the
// view_adult=true cookie, so adult works skip the interstitial).
// Only the skeleton is written; the fetched page itself is never saved.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
	"github.com/rmb938/personal-aO3-mirror/internal/version"
)

const usage = `usage: skeletonize [--config FILE] [-o OUT] <AO3 URL | file | ->

  URL    fetched with your configured User-Agent (contact.discord required), robots.txt
         checked, paced like the scraper. /works/<id> gets view_full_work=true.
  file   a saved HTML page; "-" reads stdin.
  -o     output file (default stdout).
  --retry N  retries on 5xx/Cloudflare/429 (default 4: ~15 min worst case with backoff).
`

var workOnly = regexp.MustCompile(`^/works/[0-9]+/?$`)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("skeletonize", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	cfgPath := fs.String("config", "ao3mirror.yaml", "config file (needed for URLs)")
	retries := fs.Int("retry", 4, "retries on AO3 unavailable/rate-limited responses; waits use the client's backoff (1m, 2m, 4m, 8m, ...)")
	out := fs.String("o", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Allow flags after the source too: skeletonize page.html -o out.skel.html
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	src := fs.Arg(0)
	if rest := fs.Args()[1:]; len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fs.Usage()
			return 2
		}
	}

	var page []byte
	var err error
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		page, err = fetch(*cfgPath, src, *retries)
	} else {
		page, err = readFile(src)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "skeletonize:", err)
		return 1
	}
	// The real page is not used after this point and is never written anywhere: only the
	// skeleton is.
	skel, err := skeleton.Skeletonize(strings.NewReader(string(page)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "skeletonize:", err)
		return 1
	}
	if *out == "" {
		os.Stdout.Write(skel)
		return 0
	}
	if err := os.WriteFile(*out, skel, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "skeletonize:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", *out, len(skel))
	return 0
}

func readFile(p string) ([]byte, error) {
	if p == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(p)
}

// fetch downloads one AO3 page through httpx (UA, robots, pacing, backoff, classification).
func fetch(cfgPath, raw string, retries int) ([]byte, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", cfgPath, err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	switch strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") {
	case "archiveofourown.org", "ao3.org":
	default:
		return nil, fmt.Errorf("only AO3 URLs are supported, got host %q", u.Hostname())
	}
	kind := httpx.KindPage
	if workOnly.MatchString(u.Path) {
		kind = httpx.KindWork
		q := u.Query()
		q.Set("view_full_work", "true")
		u.RawQuery = q.Encode()
	} else if strings.HasPrefix(u.Path, "/works/") {
		kind = httpx.KindWork
	}

	logger, closer, err := logx.New(logx.Options{
		Level: cfg.Log.Level, File: cfg.Path(cfg.Log.File),
		MaxBytes: int64(cfg.Log.MaxSizeMB) << 20, MaxFiles: cfg.Log.MaxFiles,
	})
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	c, err := httpx.New(httpx.Options{
		UserAgent:   cfg.UserAgent(version.Version),
		Interval:    cfg.Rate.Interval,
		Jitter:      cfg.Rate.Jitter,
		DailyBudget: cfg.Rate.DailyBudget,
		Logger:      logger.With("cmd", "skeletonize"),
	})
	if err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	attempts := retries + 1
	var resp *httpx.Response
	for i := 1; ; i++ {
		resp, err = c.Get(ctx, kind, u.RequestURI())
		if err != nil {
			return nil, err
		}
		retryable := resp.Class == httpx.ClassUnavailable || resp.Class == httpx.ClassRateLimited
		if !retryable || i >= attempts {
			break
		}
		st := c.PacerState()
		fmt.Fprintf(os.Stderr, "attempt %d/%d: AO3 returned %s (%s); retrying after backoff until %s (Ctrl+C to stop)\n",
			i, attempts, resp.Class, resp.Reason, st.BackoffTill.Local().Format("15:04:05"))
	}
	switch resp.Class {
	case httpx.ClassOK, httpx.ClassUnexpected:
		// Unexpected structure is exactly what skeletons are for; still useful.
	default:
		return nil, fmt.Errorf("AO3 returned %s (%s) after %d attempt(s); nothing written", resp.Class, resp.Reason, attempts)
	}
	if len(resp.Body) == 0 {
		return nil, fmt.Errorf("empty response (%s)", resp.Reason)
	}
	return resp.Body, nil
}
