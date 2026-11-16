package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/imagefetch"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// dateFloor is when AO3 opened; backfill windows stop here.
var dateFloor = time.Date(2008, 9, 1, 0, 0, 0, 0, time.UTC)

func sourceOf(r store.SourceRow) (ao3url.Source, error) {
	f, err := ao3url.ParseFiltersJSON(r.FiltersJSON)
	if err != nil {
		return ao3url.Source{}, err
	}
	return ao3url.Source{Kind: ao3url.Kind(r.Kind), Target: r.Target, Filters: f}, nil
}

// pageResult is what a listing page produced.
type pageResult struct {
	listing  parse.Listing
	newWorks int
	changed  int
	enqueued int
	reqID    string
}

// fetchListing fetches one listing page, stores its blurbs, links them to the source and
// enqueues work fetches for blurbs that need one.
func (s *Scheduler) fetchListing(ctx context.Context, log *slog.Logger, r store.SourceRow, src ao3url.Source,
	page int, order ao3url.Order, win *ao3url.Window, fetchClass int) (pageResult, error) {
	var res pageResult
	if !src.Kind.Sortable() {
		order, win = ao3url.Order{}, nil
	}
	path, err := ao3url.ListingPath(src, page, order, win)
	if err != nil {
		return res, permanentError{code: "bad_source:" + err.Error()}
	}
	kind := httpx.KindListing
	if src.Kind == ao3url.KindSeries {
		kind = httpx.KindSeries
	}
	resp, err := s.c.Get(ctx, kind, path)
	if err != nil {
		return res, err
	}
	res.reqID = resp.ReqID
	if err := outcome(resp); err != nil {
		if resp.Class == httpx.ClassUnexpected {
			s.saveSkeleton("listing", strconv.FormatInt(r.ID, 10)+"-p"+strconv.Itoa(page), resp.Body, log)
		}
		return res, err
	}
	fetched := s.o.Now()
	var rep *parse.Report
	if src.Kind == ao3url.KindSeries {
		var ser parse.Series
		id, _ := strconv.ParseInt(src.Target, 10, 64)
		ser, rep, err = parse.ParseSeries(id, resp.Body, fetched)
		res.listing = ser.Listing
		if err == nil && page == 1 {
			if uerr := s.st.UpsertSeries(ctx, ser.Series); uerr != nil {
				return res, uerr
			}
		}
	} else {
		res.listing, rep, err = parse.ParseListing(resp.Body, fetched)
	}
	log.Debug("parse report", append([]any{"req_id", resp.ReqID, "page", page}, rep.Attrs()...)...)
	if err != nil {
		s.saveSkeleton("listing", strconv.FormatInt(r.ID, 10)+"-p"+strconv.Itoa(page), resp.Body, log)
		return res, permanentError{code: "parse_unexpected:" + strings.Join(rep.Missing, ",")}
	}
	ids := make([]int64, 0, len(res.listing.Blurbs))
	for _, b := range res.listing.Blurbs {
		up, err := s.st.UpsertWork(ctx, b, model.FromBlurb, parse.Version)
		if err != nil {
			return res, err
		}
		ids = append(ids, b.ID)
		if up.New {
			res.newWorks++
		} else if up.Changed {
			res.changed++
		}
		if up.NeedsFetch {
			id := strconv.FormatInt(b.ID, 10)
			if _, created, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindWork, SourceID: r.ID, Target: id,
				Priority: fetchClass, DedupeKey: "work:" + id}); err != nil {
				return res, err
			} else if created {
				res.enqueued++
			}
		}
	}
	if err := s.st.LinkSourceWorks(ctx, r.ID, ids); err != nil {
		return res, err
	}
	return res, nil
}

// handlePoll walks a source newest-revised-first until a page has nothing new or changed.
func (s *Scheduler) handlePoll(ctx context.Context, job store.Job, log *slog.Logger) (map[string]any, error) {
	r, err := s.st.GetSource(ctx, job.SourceID)
	if err != nil {
		return nil, permanentError{code: "no_source"}
	}
	src, err := sourceOf(r)
	if err != nil {
		return nil, permanentError{code: "bad_filters"}
	}
	page, _ := strconv.Atoi(job.Target)
	res, err := s.fetchListing(ctx, log, r, src, page, ao3url.UpdateOrder, nil, ClassPollFetch)
	if isParseFailure(err) {
		// No retry: the next poll is a full poll_interval away.
		r.LastError = "poll page " + strconv.Itoa(page) + ": " + err.Error()
		r.LastPollAt = s.o.Now()
		if uerr := s.st.UpdateSourceProgress(ctx, r); uerr != nil {
			return nil, uerr
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	r.LastError = ""
	p := res.listing.Pagination
	more := p.Current < p.Last && page < s.o.MaxPollPages
	switch {
	case src.Kind == ao3url.KindSeries:
		// Series pages are in part order; new parts can be anywhere, so walk them all.
	case res.newWorks+res.changed == 0:
		more = false // newest-first: an unchanged page means older pages are unchanged too
	}
	if page == 1 && res.listing.Total >= 0 && res.listing.Total != r.TotalWorks {
		r.TotalWorks = res.listing.Total
	}
	if more {
		next := strconv.Itoa(page + 1)
		if _, _, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindPoll, SourceID: r.ID, Target: next,
			Priority: ClassPoll, DedupeKey: "poll:" + strconv.FormatInt(r.ID, 10) + ":" + next}); err != nil {
			return nil, err
		}
	} else {
		r.LastPollAt = s.o.Now()
	}
	if err := s.st.UpdateSourceProgress(ctx, r); err != nil {
		return nil, err
	}
	return map[string]any{"page": page, "blurbs": len(res.listing.Blurbs), "new": res.newWorks, "changed": res.changed,
		"work_jobs": res.enqueued, "continue": more, "total": res.listing.Total, "req_id": res.reqID}, nil
}

// handleBackfill walks a source in its backfill order; >MaxResults sources go window by window.
func (s *Scheduler) handleBackfill(ctx context.Context, job store.Job, log *slog.Logger) (map[string]any, error) {
	r, err := s.st.GetSource(ctx, job.SourceID)
	if err != nil {
		return nil, permanentError{code: "no_source"}
	}
	src, err := sourceOf(r)
	if err != nil {
		return nil, permanentError{code: "bad_filters"}
	}
	cur, err := store.ParseCursor(r.BackfillCursor)
	if err != nil {
		return nil, permanentError{code: "bad_cursor"}
	}
	page, _ := strconv.Atoi(job.Target)
	if cur.NextPage != page {
		return map[string]any{"stale": true, "cursor_page": cur.NextPage}, nil // cursor moved on; nothing to do
	}
	order, err := ao3url.ParseOrder(cur.Order)
	if err != nil {
		order, _ = ao3url.ParseOrder(r.BackfillOrder)
	}

	// Enter windowed mode when the source is larger than AO3's result window.
	if cur.WindowTo == "" && src.Kind.Sortable() && r.TotalWorks > s.o.MaxResults {
		to := s.o.Now().UTC()
		cur.SpanDays = s.o.WindowDays
		cur.WindowTo, cur.WindowFrom = day(to), day(to.AddDate(0, 0, -cur.SpanDays+1))
		cur.NextPage = 1
		page = 1
		log.Info("backfill windowed", "total", r.TotalWorks, "max_results", s.o.MaxResults, "span_days", cur.SpanDays)
	}
	var win *ao3url.Window
	if cur.WindowTo != "" {
		from, _ := time.Parse("2006-01-02", cur.WindowFrom)
		to, _ := time.Parse("2006-01-02", cur.WindowTo)
		win = &ao3url.Window{From: from, To: to}
	}

	fetchClass := ClassBackfill
	if cur.Rescan {
		fetchClass = ClassMaintenance
	}
	res, err := s.fetchListing(ctx, log, r, src, page, order, win, fetchClass)
	if isParseFailure(err) {
		// The same page will parse the same way: stop this source's backfill here until a new
		// parser version ships (it then resumes automatically).
		cur.BlockedParser = parse.Version
		r.BackfillCursor = cur.JSON()
		r.LastError = "backfill page " + strconv.Itoa(page) + ": " + err.Error()
		if uerr := s.st.UpdateSourceProgress(ctx, r); uerr != nil {
			return nil, uerr
		}
		log.Warn("backfill blocked on parse failure until parser update", "page", page, "parser_version", parse.Version)
		return map[string]any{"page": page, "blocked_parser": parse.Version}, err
	}
	if err != nil {
		return nil, err
	}
	cur.BlockedParser = 0
	r.LastError = ""
	detail := map[string]any{"page": page, "blurbs": len(res.listing.Blurbs), "new": res.newWorks, "rescan": cur.Rescan,
		"work_jobs": res.enqueued, "total": res.listing.Total, "req_id": res.reqID}
	if win != nil {
		detail["window_to"], detail["window_from"] = cur.WindowTo, cur.WindowFrom
	}

	maxPages := ao3url.Pages(s.o.MaxResults)
	p := res.listing.Pagination
	switch {
	case win != nil && page == 1 && res.listing.Total > s.o.MaxResults && cur.SpanDays > 1:
		// Window still too big: halve it and redo page 1 (blurbs already stored are kept).
		cur.SpanDays /= 2
		to, _ := time.Parse("2006-01-02", cur.WindowTo)
		cur.WindowFrom = day(to.AddDate(0, 0, -cur.SpanDays+1))
		cur.NextPage = 1
		detail["shrink_span_days"] = cur.SpanDays
	case p.Current < p.Last && page < maxPages:
		cur.NextPage = page + 1
	case win != nil:
		// Window done: move to the previous window.
		from, _ := time.Parse("2006-01-02", cur.WindowFrom)
		to := from.AddDate(0, 0, -1)
		if to.Before(dateFloor) {
			cur.NextPage = 0
		} else {
			// Grow back toward the configured span after shrinking.
			if cur.SpanDays < s.o.WindowDays {
				cur.SpanDays *= 2
				if cur.SpanDays > s.o.WindowDays {
					cur.SpanDays = s.o.WindowDays
				}
			}
			cur.WindowTo, cur.WindowFrom = day(to), day(to.AddDate(0, 0, -cur.SpanDays+1))
			cur.NextPage = 1
		}
	default:
		if p.Current < p.Last {
			detail["truncated_at_max_results"] = true
			log.Warn("backfill reached AO3 result window; remaining works only via windows", "page", page)
		}
		cur.NextPage = 0
	}
	if page == 1 && win == nil && res.listing.Total >= 0 {
		r.TotalWorks = res.listing.Total
	}
	r.BackfillCursor = cur.JSON()
	if err := s.st.UpdateSourceProgress(ctx, r); err != nil {
		return nil, err
	}
	detail["next_page"] = cur.NextPage
	return detail, nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// handleWork fetches one full work page and stores raw page, bodies and metadata.
func (s *Scheduler) handleWork(ctx context.Context, job store.Job, log *slog.Logger) (map[string]any, error) {
	id, err := strconv.ParseInt(job.Target, 10, 64)
	if err != nil || id <= 0 {
		return nil, permanentError{code: "bad_work_id"}
	}
	path := ao3url.WorkPath(job.Target)
	resp, err := s.c.Get(ctx, httpx.KindWork, path)
	if err != nil {
		return nil, err
	}
	detail := map[string]any{"work_id": id, "class": string(resp.Class), "bytes": len(resp.Body), "req_id": resp.ReqID}
	switch resp.Class {
	case httpx.ClassNotFound, httpx.ClassHidden, httpx.ClassRestricted:
		status := map[httpx.Class]model.WorkStatus{httpx.ClassNotFound: model.StatusDeleted,
			httpx.ClassHidden: model.StatusHidden, httpx.ClassRestricted: model.StatusRestricted}[resp.Class]
		if err := s.st.SetWorkStatus(ctx, id, status); err != nil {
			return detail, err
		}
		detail["status"] = string(status)
		log.Info("work unavailable on AO3; local copy kept", "work_id", id, "status", string(status))
		return detail, nil
	case httpx.ClassUnexpected, httpx.ClassAdultGate:
		return detail, s.workParseFailed(ctx, id, path, resp, string(resp.Class)+":"+resp.Reason, log)
	}
	if err := outcome(resp); err != nil {
		return detail, err
	}

	w, rep, err := parse.ParseWork(id, resp.Body)
	log.Debug("parse report", append([]any{"req_id", resp.ReqID, "work_id", id}, rep.Attrs()...)...)
	if err != nil {
		code := "parse_error"
		if errors.Is(err, parse.ErrUnexpected) {
			code = "parse_unexpected:" + strings.Join(rep.Missing, ",")
		}
		return detail, s.workParseFailed(ctx, id, path, resp, code, log)
	}
	// Raw page first (source of truth for reparse), then bodies, then metadata (marks fetched).
	if _, err := s.st.PutRawPage(ctx, id, path, resp.Body, s.o.KeepVersions); err != nil {
		return detail, err
	}
	change, err := s.st.PutWorkContent(ctx, id, w.Chapters, w.SkinCSS)
	if err != nil {
		return detail, err
	}
	up, err := s.st.UpsertWork(ctx, w, model.FromPage, parse.Version)
	if err != nil {
		return detail, err
	}
	if s.o.QueueImages {
		n, err := s.st.QueueImages(ctx, id, workImages(w))
		if err != nil {
			return detail, err
		}
		detail["images_queued"] = n
	}
	if job.SourceID > 0 {
		if err := s.st.LinkSourceWorks(ctx, job.SourceID, []int64{id}); err != nil {
			return detail, err
		}
	}
	detail["chapters"] = len(w.Chapters)
	detail["words"] = w.Words
	detail["content"] = string(change) // new | changed | unchanged (raw page bytes differ on nearly every fetch)
	detail["revived"] = up.Revived
	if mismatch, ok := rep.Fields["chapter_count_mismatch"]; ok {
		detail["chapter_count_mismatch"] = mismatch
	}
	return detail, nil
}

// workParseFailed handles a work page we could fetch but not parse. AO3 is fine and a retry
// would parse the same way, so: keep the raw page (next to any good versions, so reparse can
// fix it offline after a parser change), save a debug skeleton, mark the work so it is not
// re-fetched until it changes on AO3, and fail the job without retry. Existing chapter bodies
// and metadata are left untouched.
func (s *Scheduler) workParseFailed(ctx context.Context, id int64, path string, resp *httpx.Response, code string, log *slog.Logger) error {
	if len(resp.Body) > 0 {
		if _, err := s.st.PutRawPage(ctx, id, path, resp.Body, s.o.KeepVersions+1); err != nil {
			return err
		}
	}
	s.saveSkeleton("work", strconv.FormatInt(id, 10), resp.Body, log)
	if err := s.st.MarkWorkParseFailed(ctx, id); err != nil {
		return err
	}
	log.Warn("work parse failed; raw page kept for reparse, not retrying", "work_id", id, "error", code)
	return permanentError{code: code}
}

// isParseFailure reports whether err is a page-structure problem (no retry will fix it).
func isParseFailure(err error) bool {
	var pe permanentError
	return errors.As(err, &pe) && (strings.HasPrefix(pe.code, "parse_unexpected") || strings.HasPrefix(pe.code, "parse_error"))
}

// handleTagInfo fetches /tags/<tag> and records canonical/synonym/parent/meta relations.
func (s *Scheduler) handleTagInfo(ctx context.Context, job store.Job, log *slog.Logger) (map[string]any, error) {
	id, _ := strconv.ParseInt(job.Target, 10, 64)
	t, err := s.st.TagByID(ctx, id)
	if err != nil {
		return nil, permanentError{code: "no_tag"}
	}
	resp, err := s.c.Get(ctx, httpx.KindTag, "/tags/"+ao3url.TagParam(t.Name))
	if err != nil {
		return nil, err
	}
	detail := map[string]any{"tag_id": id, "class": string(resp.Class), "req_id": resp.ReqID}
	if resp.Class == httpx.ClassNotFound {
		return detail, s.st.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Missing: true})
	}
	if err := outcome(resp); err != nil {
		if resp.Class == httpx.ClassUnexpected {
			s.saveSkeleton("tag", job.Target, resp.Body, log)
			// Mark attempted so the next try waits for tags.info_ttl instead of looping.
			if uerr := s.st.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Missing: true}); uerr != nil {
				return detail, uerr
			}
		}
		return detail, err
	}
	info, rep, err := parse.ParseTag(resp.Body)
	log.Debug("parse report", append([]any{"req_id", resp.ReqID, "tag_id", id}, rep.Attrs()...)...)
	if err != nil {
		s.saveSkeleton("tag", job.Target, resp.Body, log)
		if uerr := s.st.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Missing: true}); uerr != nil {
			return detail, uerr
		}
		return detail, permanentError{code: "parse_unexpected:" + strings.Join(rep.Missing, ",")}
	}
	if err := s.st.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Type: info.Type, Canonical: info.Canonical,
		MergerOf: info.MergerOf, Parents: info.Parents, MetaTags: info.MetaTags, Synonyms: info.Synonyms}); err != nil {
		return detail, err
	}
	detail["type"], detail["canonical"], detail["parents"] = string(info.Type), info.Canonical, len(info.Parents)
	return detail, nil
}

// workImages lists embedded image URLs from a work's text (bodies, notes, summaries).
func workImages(w model.Work) []string {
	frags := []string{w.SummaryHTML, w.NotesHTML, w.EndNotesHTML}
	for _, c := range w.Chapters {
		frags = append(frags, c.BodyHTML, c.NotesHTML, c.EndNotesHTML, c.SummaryHTML)
	}
	return imagefetch.Extract(frags...)
}

// handleSeriesInfo fetches page 1 of a non-source series: description, notes, stats and
// creators. Blurbs refresh only works we already mirror (no new works are added).
func (s *Scheduler) handleSeriesInfo(ctx context.Context, job store.Job, log *slog.Logger) (map[string]any, error) {
	id, err := strconv.ParseInt(job.Target, 10, 64)
	if err != nil || id <= 0 {
		return nil, permanentError{code: "bad_series_id"}
	}
	resp, err := s.c.Get(ctx, httpx.KindSeries, "/series/"+job.Target)
	if err != nil {
		return nil, err
	}
	detail := map[string]any{"series_id": id, "class": string(resp.Class), "req_id": resp.ReqID}
	switch resp.Class {
	case httpx.ClassNotFound, httpx.ClassRestricted, httpx.ClassHidden:
		return detail, s.st.MarkSeriesFetched(ctx, id)
	}
	if err := outcome(resp); err != nil {
		if resp.Class == httpx.ClassUnexpected {
			s.saveSkeleton("series", job.Target, resp.Body, log)
			// Mark attempted so the next try waits for series.info_ttl instead of looping.
			if uerr := s.st.MarkSeriesFetched(ctx, id); uerr != nil {
				return detail, uerr
			}
		}
		return detail, err
	}
	ser, rep, err := parse.ParseSeries(id, resp.Body, s.o.Now())
	log.Debug("parse report", append([]any{"req_id", resp.ReqID, "series_id", id}, rep.Attrs()...)...)
	if err != nil {
		s.saveSkeleton("series", job.Target, resp.Body, log)
		if uerr := s.st.MarkSeriesFetched(ctx, id); uerr != nil {
			return detail, uerr
		}
		return detail, permanentError{code: "parse_unexpected:" + strings.Join(rep.Missing, ",")}
	}
	if err := s.st.UpsertSeries(ctx, ser.Series); err != nil {
		return detail, err
	}
	refreshed := 0
	for _, b := range ser.Listing.Blurbs {
		known, err := s.st.WorkExists(ctx, b.ID)
		if err != nil {
			return detail, err
		}
		if !known {
			continue
		}
		if _, err := s.st.UpsertWork(ctx, b, model.FromBlurb, parse.Version); err != nil {
			return detail, err
		}
		refreshed++
	}
	detail["works_count"], detail["known_works_refreshed"] = ser.WorksCount, refreshed
	return detail, nil
}
