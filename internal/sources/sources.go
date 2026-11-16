// Package sources manages what the mirror follows: add (with a 1-request estimate and a confirm
// gate for big sources), confirm, pause, resume, cancel, remove. The CLI uses it now; the Part 2
// API will call the same functions for on-demand "mirror this URL".
//
// Logging is content-free: source ids, kinds, hashed targets, counts.
package sources

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// EstBytesPerWork is a rough on-disk cost per mirrored work (zstd raw page + parsed bodies + meta).
const EstBytesPerWork = 60 << 10

var (
	ErrNotFound    = errors.New("sources: not found on AO3")
	ErrRestricted  = errors.New("sources: restricted or hidden on AO3 (login not supported yet)")
	ErrUnavailable = errors.New("sources: AO3 unavailable or rate limiting; try again later")
	ErrUnexpected  = errors.New("sources: unexpected page structure")
)

// ExistsError reports a duplicate add.
type ExistsError struct{ ID int64 }

func (e ExistsError) Error() string { return fmt.Sprintf("sources: already added as source %d", e.ID) }

// Fetcher is the subset of httpx.Client used here.
type Fetcher interface {
	Get(ctx context.Context, kind httpx.Kind, ref string) (*httpx.Response, error)
}

type Service struct {
	store    *store.Store
	client   Fetcher
	cfg      config.Sources
	interval time.Duration
	log      *slog.Logger
	now      func() time.Time
}

type Options struct {
	Store    *store.Store
	Client   Fetcher
	Config   config.Sources
	Interval time.Duration // current request interval, for time estimates
	Logger   *slog.Logger
	Now      func() time.Time
}

func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: o.Store, client: o.Client, cfg: o.Config, interval: o.Interval,
		log: o.Logger.With("component", "sources"), now: o.Now}
}

// AddOptions tune a new source; zero values use config defaults.
type AddOptions struct {
	Order        string        // e.g. "kudos_desc"
	Priority     int           // higher is picked first within a class
	PollInterval time.Duration // update poll interval
	Confirm      bool          // skip the auto_confirm_max gate
}

// Estimate is the projected cost of a full backfill. It contains no names.
type Estimate struct {
	Total           int
	Pages           int
	ListingRequests int // remaining listing pages (page 1 already fetched)
	WorkFetches     int
	Requests        int
	Duration        time.Duration
	DiskBytes       int64
	Windowed        bool // >100k results: backfill sliced into revised_at date windows
}

type AddResult struct {
	Source   store.SourceRow
	Estimate Estimate
	Blurbs   int // blurbs stored from page 1
}

// Add parses url, fetches page 1 once (for listing sources), stores its blurbs, estimates the
// backfill and inserts the source as active or pending_confirm.
func (s *Service) Add(ctx context.Context, rawURL string, o AddOptions) (AddResult, error) {
	src, err := ao3url.Parse(rawURL)
	if err != nil {
		return AddResult{}, err
	}
	orderName := o.Order
	if orderName == "" {
		orderName = s.cfg.DefaultBackfillOrder
	}
	order, err := ao3url.ParseOrder(orderName)
	if err != nil {
		return AddResult{}, err
	}
	poll := o.PollInterval
	if poll == 0 {
		poll = s.cfg.PollInterval
	}
	filters := ao3url.FiltersJSON(src.Filters)
	if id, err := s.store.FindSource(ctx, string(src.Kind), src.Target, filters); err != nil {
		return AddResult{}, err
	} else if id != 0 {
		return AddResult{}, ExistsError{ID: id}
	}
	base, err := ao3url.BasePath(src)
	if err != nil {
		return AddResult{}, err
	}
	log := s.log.With("kind", string(src.Kind), "target", httpx.Hash(src.Target), "filters", len(src.Filters))

	row := store.SourceRow{
		Kind: string(src.Kind), Target: src.Target, FiltersJSON: filters, Path: base,
		BackfillOrder: orderName, PollInterval: poll, Priority: o.Priority,
		NextPollAt: s.now().Add(poll),
	}
	var res AddResult

	if src.Kind == ao3url.KindWork {
		// Single work: no listing; the scheduler fetches it (1 request).
		res.Estimate = s.estimate(1, 0, 1, false)
		row.State = store.SourceActive
		row.TotalWorks, row.EstRequests = 1, res.Estimate.Requests
		row.BackfillCursor = store.Cursor{NextPage: 0, Order: orderName}.JSON()
	} else {
		total, blurbs, needFetch, err := s.fetchFirstPage(ctx, log, src, order)
		if err != nil {
			return AddResult{}, err
		}
		res.Blurbs = len(blurbs)
		pages := ao3url.Pages(total)
		windowed := total > ao3url.MaxResults
		listing := pages - 1
		if listing < 0 {
			listing = 0
		}
		// Works on page 1 that are already mirrored and unchanged need no fetch.
		fetches := total - (len(blurbs) - needFetch)
		if fetches < 0 {
			fetches = 0
		}
		res.Estimate = s.estimate(total, listing, fetches, windowed)
		row.TotalWorks, row.EstRequests = total, res.Estimate.Requests
		next := 2
		if pages <= 1 {
			next = 0
		}
		row.BackfillCursor = store.Cursor{NextPage: next, Order: orderName}.JSON()
		if o.Confirm || total <= s.cfg.AutoConfirmMax {
			row.State = store.SourceActive
		} else {
			row.State = store.SourcePendingConfirm
		}
		defer func() {
			if res.Source.ID != 0 {
				ids := make([]int64, len(blurbs))
				for i, b := range blurbs {
					ids[i] = b.ID
				}
				if err := s.store.LinkSourceWorks(ctx, res.Source.ID, ids); err != nil {
					log.Error("link source works failed", "source_id", res.Source.ID, "err", err.Error())
				}
				if n, err := s.enqueueFetches(ctx, res.Source.ID, blurbs); err != nil {
					log.Error("enqueue page-1 fetches failed", "source_id", res.Source.ID, "err", err.Error())
				} else {
					log.Debug("page-1 fetches queued", "source_id", res.Source.ID, "jobs", n)
				}
			}
		}()
	}

	id, err := s.store.InsertSource(ctx, row)
	if errors.Is(err, store.ErrSourceExists) {
		existing, _ := s.store.FindSource(ctx, row.Kind, row.Target, row.FiltersJSON)
		return AddResult{}, ExistsError{ID: existing}
	}
	if err != nil {
		return AddResult{}, err
	}
	row.ID = id
	res.Source = row
	log.Info("source added", "source_id", id, "state", string(row.State), "total_works", row.TotalWorks,
		"est_requests", res.Estimate.Requests, "est_hours", int(res.Estimate.Duration.Hours()),
		"est_disk_mb", res.Estimate.DiskBytes>>20, "windowed", res.Estimate.Windowed,
		"auto_confirm_max", s.cfg.AutoConfirmMax, "page1_blurbs", res.Blurbs)
	return res, nil
}

func (s *Service) estimate(total, listing, fetches int, windowed bool) Estimate {
	req := listing + fetches
	return Estimate{
		Total: total, Pages: ao3url.Pages(total), ListingRequests: listing, WorkFetches: fetches,
		Requests: req, Duration: time.Duration(req) * s.interval,
		DiskBytes: int64(fetches) * EstBytesPerWork, Windowed: windowed,
	}
}

// fetchFirstPage fetches and stores page 1. It returns the total, the blurbs and how many of
// them still need a full fetch.
func (s *Service) fetchFirstPage(ctx context.Context, log *slog.Logger, src ao3url.Source, order ao3url.Order) (int, []model.Work, int, error) {
	o := order
	if !src.Kind.Sortable() {
		o = ao3url.Order{}
	}
	path, err := ao3url.ListingPath(src, 1, o, nil)
	if err != nil {
		return 0, nil, 0, err
	}
	kind := httpx.KindListing
	if src.Kind == ao3url.KindSeries {
		kind = httpx.KindSeries
	}
	resp, err := s.client.Get(ctx, kind, path)
	if err != nil {
		return 0, nil, 0, err
	}
	switch resp.Class {
	case httpx.ClassOK:
	case httpx.ClassNotFound:
		return 0, nil, 0, ErrNotFound
	case httpx.ClassRestricted, httpx.ClassHidden:
		return 0, nil, 0, ErrRestricted
	case httpx.ClassRateLimited, httpx.ClassUnavailable:
		return 0, nil, 0, fmt.Errorf("%w (%s)", ErrUnavailable, resp.Reason)
	default:
		return 0, nil, 0, fmt.Errorf("%w (%s)", ErrUnexpected, resp.Reason)
	}

	fetched := s.now()
	var l parse.Listing
	var rep *parse.Report
	if src.Kind == ao3url.KindSeries {
		var ser parse.Series
		ser, rep, err = parse.ParseSeries(mustAtoi(src.Target), resp.Body, fetched)
		l = ser.Listing
		if err == nil {
			if uerr := s.store.UpsertSeries(ctx, ser.Series); uerr != nil {
				return 0, nil, 0, uerr
			}
		}
	} else {
		l, rep, err = parse.ParseListing(resp.Body, fetched)
	}
	log.Debug("parse report", append([]any{"req_id", resp.ReqID}, rep.Attrs()...)...)
	if err != nil {
		return 0, nil, 0, fmt.Errorf("%w: %v", ErrUnexpected, err)
	}
	total := l.Total
	if total < 0 {
		if l.Pagination.Last <= 1 {
			total = len(l.Blurbs)
		} else {
			return 0, nil, 0, fmt.Errorf("%w: listing total not found", ErrUnexpected)
		}
	}
	needFetch := 0
	for _, b := range l.Blurbs {
		r, err := s.store.UpsertWork(ctx, b, model.FromBlurb, parse.Version)
		if err != nil {
			return 0, nil, 0, err
		}
		if r.NeedsFetch {
			needFetch++
		}
	}
	return total, l.Blurbs, needFetch, nil
}

// enqueueFetches queues work fetches for page-1 blurbs that need one. They only run once the
// source is active (the scheduler skips jobs of pending/paused sources).
func (s *Service) enqueueFetches(ctx context.Context, sourceID int64, blurbs []model.Work) (int, error) {
	n := 0
	for _, b := range blurbs {
		need, err := s.store.WorkNeedsFetch(ctx, b.ID)
		if err != nil {
			return n, err
		}
		if !need {
			continue
		}
		if _, created, err := s.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindWork, SourceID: sourceID,
			Target: strconv.FormatInt(b.ID, 10), Priority: jobs.ClassBackfill, DedupeKey: jobs.WorkKey(b.ID)}); err != nil {
			return n, err
		} else if created {
			n++
		}
	}
	return n, nil
}

// Confirm activates a pending source.
func (s *Service) Confirm(ctx context.Context, id int64) error {
	return s.store.SetSourceState(ctx, id, store.SourceActive, store.SourcePendingConfirm)
}

// Pause stops scheduling for a source; queued jobs stay queued but are not picked.
func (s *Service) Pause(ctx context.Context, id int64) error {
	return s.store.SetSourceState(ctx, id, store.SourcePaused, store.SourceActive, store.SourceDoneBackfill, store.SourcePendingConfirm)
}

// Resume re-activates a paused source.
func (s *Service) Resume(ctx context.Context, id int64) error {
	return s.store.SetSourceState(ctx, id, store.SourceActive, store.SourcePaused)
}

// Cancel stops a source for good and drops its queued jobs. Mirrored works are kept.
func (s *Service) Cancel(ctx context.Context, id int64) (int64, error) {
	if err := s.store.SetSourceState(ctx, id, store.SourceCancelled,
		store.SourcePendingConfirm, store.SourceActive, store.SourcePaused, store.SourceDoneBackfill); err != nil {
		return 0, err
	}
	return s.store.CancelSourceJobs(ctx, id)
}

// Remove deletes a source (and its memberships). Mirrored works are kept.
func (s *Service) Remove(ctx context.Context, id int64) error { return s.store.DeleteSource(ctx, id) }

func mustAtoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
