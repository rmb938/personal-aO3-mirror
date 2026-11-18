// Package controllers holds the mirror's business operations: everything that combines several
// store calls, usually with fetching and parsing. The CLI, the scheduler and the Part 2 API call
// these. Only internal/store runs SQL.
//
//   - sources.go: add (with estimate and confirm gate), confirm, pause, resume, cancel, refresh,
//     set, remove
//   - schedule.go: which jobs each source needs now (polls, backfill pages, rescans, info fetches)
//   - crawl.go: one poll or backfill listing page (incl. date windows for huge sources)
//   - listings.go: fetch, parse and ingest one listing or series page
//   - works.go: fetch one work page, store a parsed work, queue work fetches
//   - tags.go, series.go: tag and series info pages
//   - reparse.go: rebuild parsed data from stored raw pages (zero requests)
//
// Logging is content-free: ids, counts, classes, timings, hashed names.
package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Fetcher is the subset of httpx.Client the controllers need.
type Fetcher interface {
	Get(ctx context.Context, kind httpx.Kind, ref string) (*httpx.Response, error)
}

// ErrOffline is returned by operations that need AO3 when the controllers were built without a
// client (offline commands such as reparse).
var ErrOffline = errors.New("controllers: no AO3 client (offline command)")

type Options struct {
	Store  *store.Store
	Client Fetcher // nil for offline commands; every AO3 fetch then fails with ErrOffline
	Logger *slog.Logger
	Now    func() time.Time

	DataDir      string // for data/debug skeletons
	KeepVersions int    // content.keep_versions
	QueueImages  bool   // queue embedded images of stored works for the image worker

	Sources  config.Sources // add defaults and auto-confirm gate
	Interval time.Duration  // current request interval, for add estimates

	MaxResults   int // AO3 search window (default ao3url.MaxResults); tests lower it
	MaxPollPages int // cap on pages walked per update poll (default 50)
	WindowDays   int // initial date-window span for >MaxResults sources (default 365)

	TagInfoTypes  []string      // tag types to fetch wrangling info for (empty = off)
	TagInfoTTL    time.Duration // refetch tag info after this long (default 30d)
	SeriesInfo    bool          // fetch pages of series that mirrored works belong to
	SeriesInfoTTL time.Duration // refresh those at least this often (default 30d)
}

// Controllers runs the mirror's business operations. It is safe to share; it holds no
// per-call state.
type Controllers struct {
	options Options
	store   *store.Store
	log     *slog.Logger
}

func New(opts Options) *Controllers {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.KeepVersions < 1 {
		opts.KeepVersions = 1
	}
	if opts.MaxResults == 0 {
		opts.MaxResults = ao3url.MaxResults
	}
	if opts.MaxPollPages == 0 {
		opts.MaxPollPages = 50
	}
	if opts.WindowDays == 0 {
		opts.WindowDays = 365
	}
	if opts.TagInfoTTL == 0 {
		opts.TagInfoTTL = 30 * 24 * time.Hour
	}
	if opts.SeriesInfoTTL == 0 {
		opts.SeriesInfoTTL = 30 * 24 * time.Hour
	}
	return &Controllers{options: opts, store: opts.Store, log: opts.Logger}
}

// Detail is a content-free summary of one operation (ids, counts, classes), recorded as the
// job event detail by the scheduler.
type Detail = map[string]any

// logger returns the request-scoped logger (job attrs from the scheduler) or the default one.
func (c *Controllers) logger(ctx context.Context) *slog.Logger { return logx.From(ctx, c.log) }

// ao3Get fetches one AO3 path through the paced client. It refuses to run inside a transaction
// (that would hold the SQLite write lock across a request that can wait for minutes).
func (c *Controllers) ao3Get(ctx context.Context, kind httpx.Kind, path string) (*httpx.Response, error) {
	if err := store.NotInTx(ctx); err != nil {
		return nil, err
	}
	if c.options.Client == nil {
		return nil, ErrOffline
	}
	resp, err := c.options.Client.Get(ctx, kind, path)
	if ru, ok := errors.AsType[httpx.RobotsUnavailableError](err); ok {
		// No request was sent: wait for robots.txt to be read again, without using an attempt.
		return nil, WaitError{Code: CodeRobotsUnavailable, Until: ru.Until}
	}
	switch {
	case errors.Is(err, httpx.ErrTooLarge):
		// The size will not change on a retry: fail for good (no 5 more full downloads).
		return nil, PermanentError{Code: CodeTooLarge}
	case errors.Is(err, httpx.ErrDisallowed):
		return nil, PermanentError{Code: CodeRobotsDisallowed} // no request sent; retrying won't change it
	}
	return resp, err
}

// ---- outcomes ----

// RetryError means AO3 was rate limiting or unavailable: retry after the client's backoff.
type RetryError struct{ Code string }

func (e RetryError) Error() string { return e.Code }

// WaitError means nothing was sent and the job should wait until Until (e.g. robots.txt being
// unreadable). The scheduler requeues it then without counting the attempt.
type WaitError struct {
	Code  ErrorCode
	Until time.Time
}

func (e WaitError) Error() string { return string(e.Code) }

// ErrorCode classifies a PermanentError. Codes are content-free and stable: they end up in job
// last_error and logs.
type ErrorCode string

const (
	// Page structure: no retry will fix these until the parser changes.
	CodeParseUnexpected ErrorCode = ErrorCode(httpx.ClassUnexpected) // required structure missing
	CodeParseError      ErrorCode = "parse_error"                    // page could not be parsed at all

	// AO3 answered, but not with the page (from the httpx response class).
	CodeNotFound   ErrorCode = ErrorCode(httpx.ClassNotFound)
	CodeRestricted ErrorCode = ErrorCode(httpx.ClassRestricted)
	CodeHidden     ErrorCode = ErrorCode(httpx.ClassHidden)
	CodeAdultGate  ErrorCode = ErrorCode(httpx.ClassAdultGate)
	CodeTooLarge   ErrorCode = "too_large" // body over the httpx client's MaxBody
	// CodeRobotsDisallowed: robots.txt disallows the path (no request sent).
	CodeRobotsDisallowed ErrorCode = "robots_disallowed"
	// CodeRobotsUnavailable: robots.txt could not be read, so nothing is sent for now (WaitError).
	CodeRobotsUnavailable ErrorCode = "robots_unavailable"
	// CodeOddResponse: an unusual 4xx, an unfollowed redirect or similar. Not a parse failure.
	CodeOddResponse ErrorCode = "unexpected_response"

	// Bad input or state.
	CodeBadSource   ErrorCode = "bad_source"
	CodeBadFilters  ErrorCode = "bad_filters"
	CodeBadCursor   ErrorCode = "bad_cursor"
	CodeBadWorkID   ErrorCode = "bad_work_id"
	CodeBadSeriesID ErrorCode = "bad_series_id"
	CodeNoSource    ErrorCode = "no_source"
	CodeNoTag       ErrorCode = "no_tag"

	CodePanic ErrorCode = "panic" // the job handler panicked (set by the scheduler)
)

// PermanentError means a retry cannot help (not found, parse failure, bad input).
type PermanentError struct {
	Code   ErrorCode
	Detail string // optional content-free detail, e.g. the missing selectors
}

// Error renders "code" or "code:detail".
func (e PermanentError) Error() string {
	if e.Detail == "" {
		return string(e.Code)
	}
	return string(e.Code) + ":" + e.Detail
}

// outcome maps an httpx response class to an operation outcome for non-OK responses.
func outcome(resp *httpx.Response) error {
	switch resp.Class {
	case httpx.ClassRateLimited, httpx.ClassUnavailable:
		return RetryError{Code: string(resp.Class)}
	case httpx.ClassOK:
		return nil
	case httpx.ClassUnexpected:
		if !pageStructureProblem(resp) {
			return PermanentError{Code: CodeOddResponse, Detail: resp.Reason}
		}
	}
	return PermanentError{Code: ErrorCode(resp.Class)}
}

// giveUpInfo handles a failed tag or series info fetch (scheduled every minute from what still
// needs info): a permanent error, or a retryable one on the job's last attempt, records the
// attempt via mark so the item waits for its info TTL instead of being queued again at once.
// Shutdown leaves it alone. It returns err (joined with any mark error).
// TODO: give this a better name
func giveUpInfo(ctx context.Context, err error, final bool, mark func() error) error {
	if ctx.Err() != nil || isWait(err) {
		return err // shutdown, or nothing was sent (the job is requeued): not an attempt
	}
	if _, permanent := errors.AsType[PermanentError](err); !permanent && !final {
		return err
	}
	return errors.Join(err, mark())
}

// isWait reports whether err is a WaitError: nothing was sent and the scheduler requeues the
// job without counting the attempt, so it must never be treated as giving up.
func isWait(err error) bool {
	_, ok := errors.AsType[WaitError](err)
	return ok
}

// parseUnexpected is the error for a page missing required structure (rep.Missing lists it).
func parseUnexpected(missing []string) PermanentError {
	return PermanentError{Code: CodeParseUnexpected, Detail: strings.Join(missing, ",")}
}

// pageStructureProblem reports whether an unexpected response is an AO3 page whose structure we
// did not recognise (HTTP 200), as opposed to an odd answer (unusual 4xx, unfollowed redirect,
// 1xx/3xx). Only the former is a parse failure: it is kept for reparse and blocks a backfill
// until the parser changes. An odd answer is neither stored nor treated as one.
func pageStructureProblem(resp *httpx.Response) bool {
	return resp.Class == httpx.ClassUnexpected && resp.Status == http.StatusOK
}

// IsParseFailure reports whether err is a page-structure problem (no retry will fix it).
func IsParseFailure(err error) bool {
	var pe PermanentError
	return errors.As(err, &pe) && (pe.Code == CodeParseUnexpected || pe.Code == CodeParseError)
}

// saveSkeleton writes a structure-only copy of an unexpected page to data/debug for diagnosis.
func (c *Controllers) saveSkeleton(ctx context.Context, kind string, id string, page []byte) {
	if len(page) == 0 || c.options.DataDir == "" {
		return
	}
	log := c.logger(ctx)
	rel, err := skeleton.SaveDebug(c.options.DataDir, kind, id, page, c.options.Now())
	if err != nil {
		log.Warn("debug skeleton failed", "err", err.Error())
		return
	}
	log.Warn("saved debug skeleton", "file", rel)
}
