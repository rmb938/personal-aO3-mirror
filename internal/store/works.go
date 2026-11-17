package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// UpsertResult tells the caller what changed; it drives the "re-fetch only when needed" rule.
type UpsertResult struct {
	New        bool             // work was not in the DB before
	Changed    bool             // revised date, chapter count or word count differ from stored values
	NeedsFetch bool             // full page fetch needed (never fetched, or changed since last fetch)
	PrevStatus model.WorkStatus // status before this upsert ("" if new)
	Revived    bool             // was deleted/hidden/... and is visible again
	// FetchReason (blurbs of known works that need a fetch): the listed vs stored revised date,
	// chapters and words, as slog key/value pairs. Dates and counts only, content-free.
	FetchReason []any
	// MetaChanged (re-fetches of pages fetched before): changed fields ("title") and related-row
	// changes ("tags+2-1"). Names and counts only, content-free. Stats are not compared.
	MetaChanged []string
}

type workState struct {
	revised         string
	chapters, words int
	status          model.WorkStatus
	lastFetched     int64
	fetchedRevised  string
	fetchedChapters int
	fetchedWords    int
	lastListed      int64
}

// UpsertWork merges a parsed work into meta.db.
//
// origin FromBlurb updates listing-visible metadata and stats, but never chapters, notes, the
// published date or fetch bookkeeping. FromPage updates everything and marks the work fetched.
func (s *Tx) UpsertWork(ctx context.Context, w model.Work, origin model.Origin, parserVersion int) (UpsertResult, error) {
	return s.upsert(ctx, w, origin, parserVersion, upsertOpt{})
}

// ReparseWork re-applies a work parsed from a stored raw page (fetched at pageAt). It never
// changes availability status or fetch times. Listing-visible metadata (title, tags, stats...)
// is only rewritten if no newer listing blurb has been seen since the page was fetched.
func (s *Tx) ReparseWork(ctx context.Context, w model.Work, pageAt time.Time, parserVersion int) error {
	_, err := s.upsert(ctx, w, model.FromPage, parserVersion, upsertOpt{reparse: true, pageAt: pageAt.Unix()})
	return err
}

type upsertOpt struct {
	reparse bool
	pageAt  int64
}

func (s *Tx) upsert(ctx context.Context, w model.Work, origin model.Origin, parserVersion int, opt upsertOpt) (UpsertResult, error) {
	if w.ID <= 0 {
		return UpsertResult{}, errors.New("store: work id required")
	}
	start := time.Now()
	defer s.timed("upsert_work", start, "work_id", w.ID, "origin", int(origin))
	now := s.unix()
	tx := s.meta

	var st workState
	var res UpsertResult
	err := tx.QueryRowContext(ctx, `SELECT revised_at, chapters_posted, words, status, last_fetched,
		fetched_revised, fetched_chapters, fetched_words, last_listed FROM works WHERE id = ?`, w.ID).
		Scan(&st.revised, &st.chapters, &st.words, &st.status, &st.lastFetched,
			&st.fetchedRevised, &st.fetchedChapters, &st.fetchedWords, &st.lastListed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res.New = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO works (id, first_seen, status_changed_at) VALUES (?, ?, ?)`, w.ID, now, now); err != nil {
			return res, err
		}
	case err != nil:
		return res, err
	default:
		res.PrevStatus = st.status
	}

	// Relative blurb dates ("3 days") are only day-accurate at best. Never let them overwrite a
	// known date or trigger a re-fetch on their own; chapters/words still detect updates.
	revisedComparable := !(origin == model.FromBlurb && w.RevisedApprox)
	if !revisedComparable && st.revised != "" {
		w.Revised = st.revised
	}
	// Single-chapter work pages show no revised date; their blurb date (revised_at) can differ
	// from the published date. Keep the listing's date so change detection stays consistent.
	if origin == model.FromPage && w.RevisedIsPublished && st.revised != "" {
		w.Revised = st.revised
	}
	// Listing blurbs are cached by AO3 without the time zone in the key, so their date is in the
	// time zone of whoever rendered the blurb first and can be a day off the work page's UTC date.
	// With chapters and words unchanged since the last fetch, a one-day difference is that, not
	// an update: keep the page's date, so neither a refetch nor a flip of revised_at follows.
	// (AO3's revised_at is the newest chapter's publish date; edits to text or tags don't move it.)
	if origin == model.FromBlurb && revisedComparable && st.lastFetched != 0 &&
		w.ChaptersPosted == st.fetchedChapters && w.Words == st.fetchedWords && withinOneDay(w.Revised, st.fetchedRevised) {
		w.Revised = st.fetchedRevised
	}
	if !res.New {
		res.Changed = (revisedComparable && st.revised != w.Revised) || st.chapters != w.ChaptersPosted || st.words != w.Words
	}

	// Seeing the work on AO3 means it is visible again (not when reparsing a stored page).
	if !res.New && st.status != model.StatusOK && !opt.reparse {
		res.Revived = true
		if _, err := tx.ExecContext(ctx, `UPDATE works SET status = 'ok', status_changed_at = ? WHERE id = ?`, now, w.ID); err != nil {
			return res, err
		}
	}

	// A re-fetch of a page fetched before: snapshot what it may rewrite, to report what changed.
	var before metaSnapshot
	diffPage := origin == model.FromPage && !opt.reparse && !res.New && st.lastFetched != 0
	if diffPage {
		if before, err = snapshotMeta(ctx, tx, w.ID); err != nil {
			return res, fmt.Errorf("snapshot work meta: %w", err)
		}
	}

	// A reparse of an old page must not roll back metadata from a newer listing blurb.
	listingMeta := !(opt.reparse && st.lastListed > opt.pageAt)
	if listingMeta {
		if _, err := tx.ExecContext(ctx, `UPDATE works SET title = ?, summary_html = ?, language = ?, words = ?,
		chapters_posted = ?, chapters_expected = ?, complete = ?, revised_at = ?, kudos = ?, hits = ?,
		comments = ?, bookmarks = ?, restricted = ?, anonymous = ? WHERE id = ?`,
			w.Title, w.SummaryHTML, w.Language, w.Words, w.ChaptersPosted, w.ChaptersExpected, boolInt(w.Complete),
			w.Revised, w.Stats.Kudos, w.Stats.Hits, w.Stats.Comments, w.Stats.Bookmarks, boolInt(w.Restricted),
			boolInt(w.Anonymous), w.ID); err != nil {
			return res, err
		}
	}

	switch origin {
	case model.FromBlurb:
		if _, err := tx.ExecContext(ctx, `UPDATE works SET last_listed = ? WHERE id = ?`, now, w.ID); err != nil {
			return res, err
		}
		res.NeedsFetch = res.New || st.lastFetched == 0 || (revisedComparable && st.fetchedRevised != w.Revised) ||
			st.fetchedChapters != w.ChaptersPosted || st.fetchedWords != w.Words
		if res.NeedsFetch && !res.New {
			res.FetchReason = []any{"never_fetched", st.lastFetched == 0,
				"revised_listed", w.Revised, "revised_stored", st.revised, "revised_fetched", st.fetchedRevised,
				"revised_comparable", revisedComparable,
				"chapters_listed", w.ChaptersPosted, "chapters_fetched", st.fetchedChapters,
				"words_listed", w.Words, "words_fetched", st.fetchedWords}
		}
	case model.FromPage:
		if opt.reparse {
			if _, err := tx.ExecContext(ctx, `UPDATE works SET notes_html = ?, endnotes_html = ?, published = ?,
				fetched_revised = ?, fetched_chapters = ?, fetched_words = ?, parser_version = ? WHERE id = ?`,
				w.NotesHTML, w.EndNotesHTML, w.Published, w.Revised, w.ChaptersPosted, w.Words, parserVersion, w.ID); err != nil {
				return res, err
			}
			if err := replaceChapters(ctx, tx, w.ID, w.Chapters); err != nil {
				return res, err
			}
			break
		}
		if _, err := tx.ExecContext(ctx, `UPDATE works SET notes_html = ?, endnotes_html = ?, published = ?,
			last_fetched = ?, last_checked = ?, fetched_revised = ?, fetched_chapters = ?, fetched_words = ?,
			parser_version = ? WHERE id = ?`,
			w.NotesHTML, w.EndNotesHTML, w.Published, now, now, w.Revised, w.ChaptersPosted, w.Words,
			parserVersion, w.ID); err != nil {
			return res, err
		}
		if err := replaceChapters(ctx, tx, w.ID, w.Chapters); err != nil {
			return res, err
		}
	}

	if listingMeta {
		if err := s.replaceCreators(ctx, tx, w.ID, w.Creators); err != nil {
			return res, err
		}
		if err := s.replaceTags(ctx, tx, w.ID, w.Tags); err != nil {
			return res, err
		}
		if err := s.replaceSeries(ctx, tx, w.ID, w.Series); err != nil {
			return res, err
		}
		if err := updateFTS(ctx, tx, w); err != nil {
			return res, err
		}
	}
	// Blurbs only show a collection count, so only pages (or blurbs that carry names) replace them.
	if origin == model.FromPage || len(w.Collections) > 0 {
		if err := replaceCollections(ctx, tx, w.ID, w.Collections); err != nil {
			return res, err
		}
	}
	if diffPage {
		after, err := snapshotMeta(ctx, tx, w.ID)
		if err != nil {
			return res, fmt.Errorf("snapshot work meta: %w", err)
		}
		res.MetaChanged = diffMeta(before, after)
	}
	s.log.Debug("work upserted", "work_id", w.ID, "origin", originName(origin), "reparse", opt.reparse,
		"listing_meta_updated", listingMeta, "new", res.New,
		"changed", res.Changed, "needs_fetch", res.NeedsFetch, "revived", res.Revived,
		"tags", len(w.Tags), "creators", len(w.Creators), "chapters", len(w.Chapters), "series", len(w.Series))
	return res, nil
}

func originName(o model.Origin) string {
	if o == model.FromPage {
		return "page"
	}
	return "blurb"
}

func replaceChapters(ctx context.Context, tx querier, workID int64, chs []model.Chapter) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE work_id = ?`, workID); err != nil {
		return err
	}
	for _, c := range chs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chapters (work_id, position, chapter_id, title, published)
			VALUES (?,?,?,?,?)`, workID, c.Position, c.ID, c.Title, c.Published); err != nil {
			return fmt.Errorf("chapter position %d: %w", c.Position, err)
		}
	}
	return nil
}

func (s *Tx) replaceCreators(ctx context.Context, tx querier, workID int64, cs []model.Creator) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM work_creators WHERE work_id = ?`, workID); err != nil {
		return err
	}
	for i, c := range cs {
		id, err := creatorID(ctx, tx, c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_creators (work_id, creator_id, position) VALUES (?,?,?)`,
			workID, id, i); err != nil {
			return err
		}
	}
	return nil
}

func creatorID(ctx context.Context, tx querier, c model.Creator) (int64, error) {
	pseud := c.Pseud
	if pseud == "" {
		pseud = c.User
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO creators (user_login, pseud) VALUES (?, ?)
		ON CONFLICT (user_login, pseud) DO UPDATE SET user_login = excluded.user_login RETURNING id`,
		c.User, pseud).Scan(&id)
	return id, err
}

// TagID returns the local id for name, creating it. A known type replaces "Unknown".
func (s *Tx) tagID(ctx context.Context, tx querier, t model.Tag) (int64, error) {
	typ := t.Type
	if typ == "" {
		typ = model.TagUnknown
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO tags (name, type, first_seen) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET type = CASE WHEN tags.type = 'Unknown' THEN excluded.type ELSE tags.type END
		RETURNING id`, t.Name, string(typ), s.unix()).Scan(&id)
	return id, err
}

func (s *Tx) replaceTags(ctx context.Context, tx querier, workID int64, tags []model.Tag) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM work_tags WHERE work_id = ?`, workID); err != nil {
		return err
	}
	for i, t := range tags {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		id, err := s.tagID(ctx, tx, t)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_tags (work_id, tag_id, position) VALUES (?,?,?)`,
			workID, id, i); err != nil {
			return err
		}
	}
	return nil
}

func (s *Tx) replaceSeries(ctx context.Context, tx querier, workID int64, parts []model.SeriesPart) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM series_works WHERE work_id = ?`, workID); err != nil {
		return err
	}
	for _, p := range parts {
		if p.ID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO series (id, title, first_seen) VALUES (?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET title = CASE WHEN excluded.title != '' THEN excluded.title ELSE series.title END`,
			p.ID, p.Title, s.unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO series_works (series_id, work_id, position) VALUES (?,?,?)`,
			p.ID, workID, p.Position); err != nil {
			return err
		}
	}
	return nil
}

func replaceCollections(ctx context.Context, tx querier, workID int64, cs []model.Collection) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM work_collections WHERE work_id = ?`, workID); err != nil {
		return err
	}
	for _, c := range cs {
		if c.Name == "" {
			continue
		}
		var id int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO collections (name, title) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET title = CASE WHEN excluded.title != '' THEN excluded.title ELSE collections.title END
			RETURNING id`, c.Name, c.Title).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_collections (work_id, collection_id) VALUES (?,?)`, workID, id); err != nil {
			return err
		}
	}
	return nil
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainText strips HTML for indexing.
func plainText(h string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(h, " "))), " ")
}

func updateFTS(ctx context.Context, tx querier, w model.Work) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM works_fts WHERE rowid = ?`, w.ID); err != nil {
		return err
	}
	creators := make([]string, 0, len(w.Creators))
	for _, c := range w.Creators {
		creators = append(creators, c.User, c.Pseud)
	}
	tags := make([]string, 0, len(w.Tags))
	for _, t := range w.Tags {
		tags = append(tags, t.Name)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO works_fts (rowid, title, summary, creators, tags) VALUES (?,?,?,?,?)`,
		w.ID, w.Title, plainText(w.SummaryHTML), strings.Join(creators, " "), strings.Join(tags, " • "))
	return err
}

// SearchWorkIDs runs an FTS5 MATCH query and returns work ids by rank.
func (s *ReadTx) SearchWorkIDs(ctx context.Context, match string, limit int) ([]int64, error) {
	return queryInt64s(ctx, s.meta, `SELECT rowid FROM works_fts WHERE works_fts MATCH ? ORDER BY rank LIMIT ?`, match, limit)
}

// SetWorkStatus records a verification result (deleted/hidden/restricted/...). Content is kept.
func (s *Tx) SetWorkStatus(ctx context.Context, workID int64, status model.WorkStatus) error {
	now := s.unix()
	res, err := s.meta.ExecContext(ctx, `UPDATE works SET
		status_changed_at = CASE WHEN status != ? THEN ? ELSE status_changed_at END,
		status = ?, last_checked = ? WHERE id = ?`, string(status), now, string(status), now, workID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Unknown work (e.g. on-demand fetch of a deleted id): record it so we don't retry blindly.
		_, err = s.meta.ExecContext(ctx, `INSERT INTO works (id, status, status_changed_at, first_seen, last_checked)
			VALUES (?,?,?,?,?)`, workID, string(status), now, now, now)
	}
	s.log.Debug("work status set", "work_id", workID, "status", string(status))
	return err
}

// WorkStatus returns a work's status and whether it exists.
func (s *ReadTx) WorkStatus(ctx context.Context, workID int64) (model.WorkStatus, bool, error) {
	var st model.WorkStatus
	err := s.meta.QueryRowContext(ctx, `SELECT status FROM works WHERE id = ?`, workID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return st, err == nil, err
}

// ErrNoWork means no works row has the requested id.
var ErrNoWork = errors.New("store: no such work")

// Work returns a work's own metadata and stats (the works row). Related rows (creators, tags,
// series, collections, chapters) are not loaded. The result holds fanwork content: never log it.
func (s *ReadTx) Work(ctx context.Context, id int64) (model.Work, error) {
	w := model.Work{ID: id}
	var complete, restricted, anonymous int
	err := s.meta.QueryRowContext(ctx, `SELECT title, summary_html, notes_html, endnotes_html, language, words,
		chapters_posted, chapters_expected, complete, published, revised_at, kudos, hits, comments, bookmarks,
		restricted, anonymous FROM works WHERE id = ?`, id).Scan(&w.Title, &w.SummaryHTML, &w.NotesHTML,
		&w.EndNotesHTML, &w.Language, &w.Words, &w.ChaptersPosted, &w.ChaptersExpected, &complete, &w.Published,
		&w.Revised, &w.Stats.Kudos, &w.Stats.Hits, &w.Stats.Comments, &w.Stats.Bookmarks, &restricted, &anonymous)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNoWork
	}
	w.Complete, w.Restricted, w.Anonymous = complete == 1, restricted == 1, anonymous == 1
	return w, err
}

// WorkDiag is the content-free state of one work (for `debug work` and tests): status, dates,
// numbers, bookkeeping and row counts. It never holds titles, summaries, notes, names or tags.
type WorkDiag struct {
	Status           model.WorkStatus
	Published        string // YYYY-MM-DD
	Revised          string // YYYY-MM-DD
	FetchedRevised   string // revised date at the last full fetch
	Words            int
	ChaptersPosted   int
	ChaptersExpected int // 0 = unknown
	Stats            model.Stats
	Restricted       bool
	Anonymous        bool
	ParserVersion    int
	FirstSeen        time.Time
	LastListed       time.Time
	LastFetched      time.Time
	LastChecked      time.Time
	// Related row counts.
	Tags          int
	Creators      int
	ChapterRows   int
	Sources       int
	Images        int
	RawVersions   int   // content.db raw_pages rows
	RawBytes      int64 // uncompressed size of those pages
	ChapterBodies int   // content.db chapter_bodies rows
}

// WorkDiag returns the content-free diagnostic view of a work, or ErrNoWork.
func (s *ReadTx) WorkDiag(ctx context.Context, id int64) (WorkDiag, error) {
	var d WorkDiag
	var restricted, anonymous int
	var firstSeen, lastListed, lastFetched, lastChecked int64
	err := s.meta.QueryRowContext(ctx, `SELECT status, published, revised_at, fetched_revised, words, chapters_posted,
		chapters_expected, kudos, hits, comments, bookmarks, restricted, anonymous, parser_version, first_seen, last_listed,
		last_fetched, last_checked,
		(SELECT COUNT(*) FROM work_tags WHERE work_id = works.id),
		(SELECT COUNT(*) FROM work_creators WHERE work_id = works.id),
		(SELECT COUNT(*) FROM chapters WHERE work_id = works.id),
		(SELECT COUNT(*) FROM source_works WHERE work_id = works.id),
		(SELECT COUNT(*) FROM work_images WHERE work_id = works.id)
		FROM works WHERE id = ?`, id).Scan(&d.Status, &d.Published, &d.Revised, &d.FetchedRevised, &d.Words,
		&d.ChaptersPosted, &d.ChaptersExpected, &d.Stats.Kudos, &d.Stats.Hits, &d.Stats.Comments, &d.Stats.Bookmarks,
		&restricted, &anonymous, &d.ParserVersion, &firstSeen, &lastListed, &lastFetched, &lastChecked,
		&d.Tags, &d.Creators, &d.ChapterRows, &d.Sources, &d.Images)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNoWork
	}
	if err != nil {
		return d, err
	}
	d.Restricted, d.Anonymous = restricted == 1, anonymous == 1
	d.FirstSeen, d.LastListed = unixTime(firstSeen), unixTime(lastListed)
	d.LastFetched, d.LastChecked = unixTime(lastFetched), unixTime(lastChecked)
	err = s.content.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM raw_pages WHERE work_id = ?),
		(SELECT COALESCE(SUM(size), 0) FROM raw_pages WHERE work_id = ?),
		(SELECT COUNT(*) FROM chapter_bodies WHERE work_id = ?)`, id, id, id).
		Scan(&d.RawVersions, &d.RawBytes, &d.ChapterBodies)
	return d, err
}

// WorkExists reports whether a work row exists.
func (s *ReadTx) WorkExists(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM works WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}

// WorkParserVersion returns the parser version a work was last parsed with (0 if unknown).
func (s *ReadTx) WorkParserVersion(ctx context.Context, workID int64) (int, error) {
	var v int
	err := s.meta.QueryRowContext(ctx, `SELECT parser_version FROM works WHERE id = ?`, workID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// MarkWorkParseFailed records that a work's page was fetched but could not be parsed. The work
// is marked fetched (so listings stop asking for it until it changes on AO3) with
// parser_version 0 (so `reparse` retries it from the stored raw page after a parser fix).
// Existing content and metadata are kept.
func (s *Tx) MarkWorkParseFailed(ctx context.Context, workID int64) error {
	now := s.unix()
	if _, err := s.meta.ExecContext(ctx, `INSERT INTO works (id, first_seen, status_changed_at) VALUES (?,?,?)
		ON CONFLICT (id) DO NOTHING`, workID, now, now); err != nil {
		return err
	}
	_, err := s.meta.ExecContext(ctx, `UPDATE works SET last_fetched = ?, last_checked = ?, fetched_revised = revised_at,
		fetched_chapters = chapters_posted, fetched_words = words, parser_version = 0 WHERE id = ?`, now, now, workID)
	s.log.Debug("work parse failure recorded", "work_id", workID)
	return err
}
