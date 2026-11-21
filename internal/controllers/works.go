package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/imagefetch"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// EnqueueWorkFetch queues a full fetch of one work. Fetches are deduplicated per work: if one is
// already queued or running, its class is raised to class when that is more urgent. It returns
// the job id and whether a new job was created. sourceID 0 means no source (on-demand).
func (c *Controllers) EnqueueWorkFetch(ctx context.Context, workID, sourceID int64, class int) (int64, bool, error) {
	var jobID int64
	var created bool
	err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		jobID, created, err = enqueueWorkFetch(ctx, tx, workID, sourceID, class)
		return err
	})
	return jobID, created, err
}

// enqueueWorkFetch is EnqueueWorkFetch inside the caller's transaction: the one place that
// builds work-fetch jobs.
func enqueueWorkFetch(ctx context.Context, tx *store.Tx, workID, sourceID int64, class int) (int64, bool, error) {
	return tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindWork, SourceID: sourceID,
		Target: strconv.FormatInt(workID, 10), Priority: class, DedupeKey: jobs.WorkKey(workID)})
}

// FetchWork fetches one full work page and stores the raw page, chapter bodies and metadata in
// one transaction. A work that is gone, hidden or restricted on AO3 is marked so; the local copy
// is kept. A fetch that gives up (a permanent error, or a retryable one on the job's last
// attempt: final) records fetch_failed_at, so polls stop re-queueing a work that keeps failing.
//
// A successful fetch also settles rescans that missed the work: it is on AO3, so any source
// whose last MissedRescansToDrop rescans did not list it has lost it.
func (c *Controllers) FetchWork(ctx context.Context, id, sourceID int64, final bool) (*WorkDetail, error) {
	detail, err := c.fetchWork(ctx, id, sourceID)
	if err == nil || id <= 0 || IsParseFailure(err) || isTooLarge(err) {
		return detail, err // parse failures and too-large pages are recorded with their own marks
	}
	return detail, markIfGivingUp(ctx, err, final, func() error {
		return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			return tx.MarkWorkFetchFailed(ctx, id)
		})
	})
}

func (c *Controllers) fetchWork(ctx context.Context, id, sourceID int64) (*WorkDetail, error) {
	if id <= 0 {
		return nil, PermanentError{Code: CodeBadWorkID}
	}
	log := c.logger(ctx)
	path := ao3url.WorkPath(strconv.FormatInt(id, 10))
	resp, err := c.ao3Get(ctx, httpx.KindWork, path)
	if isTooLarge(err) {
		// Marked fetched so listings don't queue another huge download until the work changes on
		// AO3 (needs a higher limit, then a refresh).
		if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			return tx.MarkWorkTooLarge(ctx, id)
		}); err != nil {
			return nil, err
		}
		log.Warn("work page over max body; not retrying until it changes", "work_id", id)
		return &WorkDetail{WorkID: new(id)}, err
	}
	if err != nil {
		return nil, err
	}
	detail := &WorkDetail{WorkID: new(id), Class: new(string(resp.Class)), Bytes: new(len(resp.Body)), ReqID: new(resp.ReqID)}
	switch resp.Class {
	case httpx.ClassNotFound, httpx.ClassHidden, httpx.ClassRestricted:
		status := map[httpx.Class]model.WorkStatus{httpx.ClassNotFound: model.StatusDeleted,
			httpx.ClassHidden: model.StatusHidden, httpx.ClassRestricted: model.StatusRestricted}[resp.Class]
		if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			return tx.SetWorkStatus(ctx, id, status)
		}); err != nil {
			return detail, err
		}
		detail.Status = new(string(status))
		log.Info("work unavailable on AO3; local copy kept", "work_id", id, "status", string(status))
		return detail, nil
	case httpx.ClassAdultGate:
		// A cookie/session problem, not this page's structure: store nothing and don't mark the
		// work fetched (a reparse of the interstitial could never fix it). FetchWork records the
		// failure, so a listing poll or rescan queues it again once store.FailedFetchBackoff has
		// passed, or sooner if the work changes on AO3.
		log.Warn("adult interstitial despite view_adult cookie; work left unfetched", "work_id", id, "req_id", resp.ReqID)
		return detail, PermanentError{Code: CodeAdultGate, Detail: resp.Reason}
	case httpx.ClassUnexpected:
		if !pageStructureProblem(resp) {
			// An odd answer (an unusual 4xx, an unfollowed redirect), not a page we failed to
			// parse: store nothing and leave the work unfetched for a listing to queue again
			// after the failed-fetch backoff.
			log.Warn("unexpected AO3 response; work left unfetched", "work_id", id, "status", resp.Status, "reason", resp.Reason)
			return detail, PermanentError{Code: CodeOddResponse, Detail: resp.Reason}
		}
		return detail, c.workParseFailed(ctx, id, path, resp, PermanentError{Code: ErrorCode(resp.Class), Detail: resp.Reason})
	}
	if err := outcome(resp); err != nil {
		return detail, err
	}

	w, rep, err := parseWork(log, id, resp.Body)
	log.Debug("parse report", append([]any{"req_id", resp.ReqID, "work_id", id}, rep.Attrs()...)...)
	if err != nil {
		pe := PermanentError{Code: CodeParseError}
		if errors.Is(err, errParserPanic) {
			pe.Detail = "panic"
		} else if errors.Is(err, parse.ErrUnexpected) {
			pe = parseUnexpected(rep.Missing)
		}
		return detail, c.workParseFailed(ctx, id, path, resp, pe)
	}
	var sw storedWork
	var leftSources int64
	if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		// Raw page (source of truth for reparse), bodies, then metadata (marks fetched). content.db
		// commits before meta.db, so a work is never marked fetched without its page.
		if _, err := tx.PutRawPage(ctx, id, path, resp.Body, c.options.KeepVersions); err != nil {
			return err
		}
		var err error
		if sw, err = c.storeWork(ctx, tx, w, false, time.Time{}); err != nil {
			return err
		}
		if sourceID > 0 {
			// The source may have been removed while we fetched: keep the page (the request is
			// spent, and removing a source keeps its mirrored works), just don't link it.
			exists, err := tx.SourceExists(ctx, sourceID)
			if err != nil {
				return err
			}
			// A membership, but not a listing sighting: an existing one keeps its last_seen and
			// missed_rescans, so this fetch never keeps a work its source stopped listing.
			if exists {
				if err := tx.AddSourceWork(ctx, sourceID, id); err != nil {
					return err
				}
			}
		}
		// The work is on AO3: sources whose recent rescans all missed it have lost it.
		leftSources, err = tx.DropMissedSourceWorks(ctx, id)
		return err
	}); errors.Is(err, errChaptersShrank) {
		// Rolled back: handled like any parse failure (page kept for reparse, no retry).
		return detail, c.workParseFailed(ctx, id, path, resp, PermanentError{Code: CodeParseError, Detail: "chapters_shrank"})
	} else if err != nil {
		return detail, err
	}
	if leftSources > 0 {
		detail.LeftSources = new(leftSources)
		log.Info("work left sources that stopped listing it", "work_id", id, "dropped_members", leftSources)
	}
	if c.options.QueueImages {
		detail.ImagesQueued = new(sw.images)
	}
	detail.Chapters = new(len(w.Chapters))
	detail.Words = new(w.Words)
	detail.Content = new(string(sw.content)) // new | changed | unchanged (raw page bytes differ on nearly every fetch)
	detail.Revived = new(sw.upsert.Revived)
	if sw.upsert.MetaChanged != nil {
		detail.MetaChanged = sw.upsert.MetaChanged // field names and +added/-removed counts only
	}
	if mismatch, ok := rep.Fields["chapter_count_mismatch"].(bool); ok {
		detail.ChapterCountMismatch = new(mismatch)
	}
	return detail, nil
}

// storedWork is what storing a parsed work page changed.
type storedWork struct {
	content store.ContentChange
	upsert  store.UpsertResult // zero value for a reparse
	images  int                // images newly queued
}

// storeWork writes a parsed work page in tx: chapter bodies and skin, then metadata, then queues
// its embedded images. A fresh fetch (reparse false) marks the work fetched. A reparse leaves
// fetch bookkeeping and status alone; pageAt is when the stored page was fetched, so newer
// listing metadata is not rolled back.
//
// A page with fewer chapters than it says are posted, and fewer than are stored, is refused
// with errChaptersShrank (the stored chapters are kept): the parser most likely missed some.
// A page that is merely short of its posted count is stored; the gap (fetched_chapters vs
// stored chapters) is left for the UI to warn about. A chapter the author deleted lowers the
// posted count too, so it is not refused.
func (c *Controllers) storeWork(ctx context.Context, tx *store.Tx, w model.Work, reparse bool, pageAt time.Time) (storedWork, error) {
	var sw storedWork
	var err error
	if parsed := len(w.Chapters); parsed < w.ChaptersPosted {
		stored, err := tx.ChapterCount(ctx, w.ID)
		if err != nil {
			return sw, err
		}
		if parsed < stored {
			c.logger(ctx).Warn("work page has fewer chapters than stored and than posted; stored chapters kept",
				"work_id", w.ID, "chapters_parsed", parsed, "chapters_posted", w.ChaptersPosted, "chapters_stored", stored,
				"reparse", reparse)
			return sw, errChaptersShrank
		}
	}
	if sw.content, err = tx.PutWorkContent(ctx, w.ID, w.Chapters, w.SkinCSS); err != nil {
		return sw, err
	}
	if reparse {
		err = tx.ReparseWork(ctx, w, pageAt, parse.Version)
	} else {
		sw.upsert, err = tx.UpsertWork(ctx, w, model.FromPage, parse.Version)
	}
	if err != nil {
		return sw, err
	}
	if c.options.QueueImages {
		if sw.images, err = tx.QueueImages(ctx, w.ID, imagefetch.WorkImages(w)); err != nil {
			return sw, err
		}
	}
	return sw, nil
}

// isTooLarge reports whether err is a work page over the body limit.
func isTooLarge(err error) bool {
	pe, ok := errors.AsType[PermanentError](err)
	return ok && pe.Code == CodeTooLarge
}

// errChaptersShrank refuses a parsed work page that would replace stored chapters with fewer,
// while the page itself says more are posted (see storeWork).
var errChaptersShrank = errors.New("work page parsed fewer chapters than stored and than posted")

// errParserPanic is a parser panic on a work page.
var errParserPanic = errors.New("parser panicked")

// parseWork is parse.ParseWork with a parser panic turned into an error, so the page is handled
// like any parse failure (kept for reparse, work marked, no retry) instead of failing the job
// unmarked and being queued again by every poll. Only the panic's type is logged: its value
// could carry page content.
func parseWork(log *slog.Logger, id int64, page []byte) (w model.Work, rep *parse.Report, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("work parser panicked", "work_id", id, "panic_type", fmt.Sprintf("%T", r))
			w, rep, err = model.Work{ID: id}, &parse.Report{}, errParserPanic
		}
	}()
	return parse.ParseWork(id, page)
}

// workParseFailed handles a work page we could fetch but not parse. AO3 is fine and a retry
// would parse the same way, so: keep the raw page (next to any good versions, so reparse can
// fix it offline after a parser change), save a debug skeleton, mark the work so it is not
// re-fetched until it changes on AO3, and fail without retry. Existing chapter bodies and
// metadata are left untouched.
func (c *Controllers) workParseFailed(ctx context.Context, id int64, path string, resp *httpx.Response, pe PermanentError) error {
	c.saveSkeleton(ctx, "work", strconv.FormatInt(id, 10), resp.Body)
	if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		if len(resp.Body) > 0 {
			if _, err := tx.PutFailedRawPage(ctx, id, path, resp.Body, c.options.KeepVersions); err != nil {
				return err
			}
		}
		return tx.MarkWorkParseFailed(ctx, id)
	}); err != nil {
		return err
	}
	c.logger(ctx).Warn("work parse failed; raw page kept for reparse, not retrying", "work_id", id, "error", pe.Error())
	return pe
}
