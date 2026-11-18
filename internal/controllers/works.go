package controllers

import (
	"context"
	"errors"
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
	return tx.EnqueueJob(ctx, store.Job{Kind: jobs.KindWork, SourceID: sourceID,
		Target: strconv.FormatInt(workID, 10), Priority: class, DedupeKey: jobs.WorkKey(workID)})
}

// FetchWork fetches one full work page and stores the raw page, chapter bodies and metadata in
// one transaction. A work that is gone, hidden or restricted on AO3 is marked so; the local copy
// is kept. A failed fetch (anything but shutdown or a robots wait) records fetch_failed_at, so
// polls stop re-queueing a work that keeps failing; the job's own retries are unaffected.
func (c *Controllers) FetchWork(ctx context.Context, id, sourceID int64) (Detail, error) {
	detail, err := c.fetchWork(ctx, id, sourceID)
	if err == nil || id <= 0 || ctx.Err() != nil || isWait(err) {
		return detail, err
	}
	if merr := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		return tx.MarkWorkFetchFailed(ctx, id)
	}); merr != nil {
		return detail, errors.Join(err, merr)
	}
	return detail, err
}

func (c *Controllers) fetchWork(ctx context.Context, id, sourceID int64) (Detail, error) {
	if id <= 0 {
		return nil, PermanentError{Code: CodeBadWorkID}
	}
	log := c.logger(ctx)
	path := ao3url.WorkPath(strconv.FormatInt(id, 10))
	resp, err := c.ao3Get(ctx, httpx.KindWork, path)
	if pe, ok := errors.AsType[PermanentError](err); ok && pe.Code == CodeTooLarge {
		// Marked fetched so listings don't queue another huge download until the work changes on
		// AO3 (needs a higher limit, then a refresh).
		if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			return tx.MarkWorkTooLarge(ctx, id)
		}); err != nil {
			return nil, err
		}
		log.Warn("work page over max body; not retrying until it changes", "work_id", id)
		return Detail{"work_id": id}, pe
	}
	if err != nil {
		return nil, err
	}
	detail := Detail{"work_id": id, "class": string(resp.Class), "bytes": len(resp.Body), "req_id": resp.ReqID}
	switch resp.Class {
	case httpx.ClassNotFound, httpx.ClassHidden, httpx.ClassRestricted:
		status := map[httpx.Class]model.WorkStatus{httpx.ClassNotFound: model.StatusDeleted,
			httpx.ClassHidden: model.StatusHidden, httpx.ClassRestricted: model.StatusRestricted}[resp.Class]
		if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			return tx.SetWorkStatus(ctx, id, status)
		}); err != nil {
			return detail, err
		}
		detail["status"] = string(status)
		log.Info("work unavailable on AO3; local copy kept", "work_id", id, "status", string(status))
		return detail, nil
	case httpx.ClassAdultGate:
		// A cookie/session problem, not this page's structure: store nothing and don't mark the
		// work fetched, so it is queued again by the next listing poll or rescan once the
		// interstitial stops (which a reparse of the interstitial never could).
		log.Warn("adult interstitial despite view_adult cookie; work left unfetched", "work_id", id, "req_id", resp.ReqID)
		return detail, PermanentError{Code: CodeAdultGate, Detail: resp.Reason}
	case httpx.ClassUnexpected:
		if !pageStructureProblem(resp) {
			// An odd answer (an unusual 4xx, an unfollowed redirect), not a page we failed to
			// parse: store nothing and leave the work unfetched for the next listing to queue.
			log.Warn("unexpected AO3 response; work left unfetched", "work_id", id, "status", resp.Status, "reason", resp.Reason)
			return detail, PermanentError{Code: CodeOddResponse, Detail: resp.Reason}
		}
		return detail, c.workParseFailed(ctx, id, path, resp, PermanentError{Code: ErrorCode(resp.Class), Detail: resp.Reason})
	}
	if err := outcome(resp); err != nil {
		return detail, err
	}

	w, rep, err := parse.ParseWork(id, resp.Body)
	log.Debug("parse report", append([]any{"req_id", resp.ReqID, "work_id", id}, rep.Attrs()...)...)
	if err != nil {
		pe := PermanentError{Code: CodeParseError}
		if errors.Is(err, parse.ErrUnexpected) {
			pe = parseUnexpected(rep.Missing)
		}
		return detail, c.workParseFailed(ctx, id, path, resp, pe)
	}
	var sw storedWork
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
		if sourceID <= 0 {
			return nil
		}
		// The source may have been removed while we fetched: keep the page (the request is
		// spent, and removing a source keeps its mirrored works), just don't link it.
		exists, err := tx.SourceExists(ctx, sourceID)
		if err != nil || !exists {
			return err
		}
		// A membership, but not a listing sighting: last_seen is left alone, so a rescan's
		// verification fetch never keeps a work that left the source.
		return tx.AddSourceWork(ctx, sourceID, id)
	}); err != nil {
		return detail, err
	}
	if c.options.QueueImages {
		detail["images_queued"] = sw.images
	}
	detail["chapters"] = len(w.Chapters)
	detail["words"] = w.Words
	detail["content"] = string(sw.content) // new | changed | unchanged (raw page bytes differ on nearly every fetch)
	detail["revived"] = sw.upsert.Revived
	if sw.upsert.MetaChanged != nil {
		detail["meta_changed"] = sw.upsert.MetaChanged // field names and +added/-removed counts only
	}
	if mismatch, ok := rep.Fields["chapter_count_mismatch"]; ok {
		detail["chapter_count_mismatch"] = mismatch
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
func (c *Controllers) storeWork(ctx context.Context, tx *store.Tx, w model.Work, reparse bool, pageAt time.Time) (storedWork, error) {
	var sw storedWork
	var err error
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
