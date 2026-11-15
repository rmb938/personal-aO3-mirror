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
}

type workState struct {
	revised         string
	chapters, words int
	status          model.WorkStatus
	lastFetched     int64
	fetchedRevised  string
	fetchedChapters int
	fetchedWords    int
}

// UpsertWork merges a parsed work into meta.db.
//
// origin FromBlurb updates listing-visible metadata and stats, but never chapters, notes, the
// published date or fetch bookkeeping. FromPage updates everything and marks the work fetched.
func (s *Store) UpsertWork(ctx context.Context, w model.Work, origin model.Origin, parserVersion int) (UpsertResult, error) {
	if w.ID <= 0 {
		return UpsertResult{}, errors.New("store: work id required")
	}
	start := time.Now()
	defer s.timed("upsert_work", start, "work_id", w.ID, "origin", int(origin))
	now := s.unix()
	tx, err := s.Meta.BeginTx(ctx, nil)
	if err != nil {
		return UpsertResult{}, err
	}
	defer tx.Rollback()

	var st workState
	var res UpsertResult
	err = tx.QueryRowContext(ctx, `SELECT revised_at, chapters_posted, words, status, last_fetched,
		fetched_revised, fetched_chapters, fetched_words FROM works WHERE id = ?`, w.ID).
		Scan(&st.revised, &st.chapters, &st.words, &st.status, &st.lastFetched,
			&st.fetchedRevised, &st.fetchedChapters, &st.fetchedWords)
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
	if !res.New {
		res.Changed = (revisedComparable && st.revised != w.Revised) || st.chapters != w.ChaptersPosted || st.words != w.Words
	}

	// Seeing the work on AO3 means it is visible again.
	if !res.New && st.status != model.StatusOK {
		res.Revived = true
		if _, err := tx.ExecContext(ctx, `UPDATE works SET status = 'ok', status_changed_at = ? WHERE id = ?`, now, w.ID); err != nil {
			return res, err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE works SET title = ?, summary_html = ?, language = ?, words = ?,
		chapters_posted = ?, chapters_expected = ?, complete = ?, revised_at = ?, kudos = ?, hits = ?,
		comments = ?, bookmarks = ?, restricted = ?, anonymous = ? WHERE id = ?`,
		w.Title, w.SummaryHTML, w.Language, w.Words, w.ChaptersPosted, w.ChaptersExpected, boolInt(w.Complete),
		w.Revised, w.Stats.Kudos, w.Stats.Hits, w.Stats.Comments, w.Stats.Bookmarks, boolInt(w.Restricted),
		boolInt(w.Anonymous), w.ID); err != nil {
		return res, err
	}

	switch origin {
	case model.FromBlurb:
		if _, err := tx.ExecContext(ctx, `UPDATE works SET last_listed = ? WHERE id = ?`, now, w.ID); err != nil {
			return res, err
		}
		res.NeedsFetch = res.New || st.lastFetched == 0 || (revisedComparable && st.fetchedRevised != w.Revised) ||
			st.fetchedChapters != w.ChaptersPosted || st.fetchedWords != w.Words
	case model.FromPage:
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

	if err := s.replaceCreators(ctx, tx, w.ID, w.Creators); err != nil {
		return res, err
	}
	if err := s.replaceTags(ctx, tx, w.ID, w.Tags); err != nil {
		return res, err
	}
	if err := s.replaceSeries(ctx, tx, w.ID, w.Series); err != nil {
		return res, err
	}
	// Blurbs only show a collection count, so only pages (or blurbs that carry names) replace them.
	if origin == model.FromPage || len(w.Collections) > 0 {
		if err := replaceCollections(ctx, tx, w.ID, w.Collections); err != nil {
			return res, err
		}
	}
	if err := updateFTS(ctx, tx, w); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	s.log.Debug("work upserted", "work_id", w.ID, "origin", originName(origin), "new", res.New,
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

func replaceChapters(ctx context.Context, tx *sql.Tx, workID int64, chs []model.Chapter) error {
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

func (s *Store) replaceCreators(ctx context.Context, tx *sql.Tx, workID int64, cs []model.Creator) error {
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

func creatorID(ctx context.Context, tx *sql.Tx, c model.Creator) (int64, error) {
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
func (s *Store) tagID(ctx context.Context, tx *sql.Tx, t model.Tag) (int64, error) {
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

func (s *Store) replaceTags(ctx context.Context, tx *sql.Tx, workID int64, tags []model.Tag) error {
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

func (s *Store) replaceSeries(ctx context.Context, tx *sql.Tx, workID int64, parts []model.SeriesPart) error {
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

func replaceCollections(ctx context.Context, tx *sql.Tx, workID int64, cs []model.Collection) error {
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

// PlainText strips HTML for indexing.
func PlainText(h string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(h, " "))), " ")
}

func updateFTS(ctx context.Context, tx *sql.Tx, w model.Work) error {
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
		w.ID, w.Title, PlainText(w.SummaryHTML), strings.Join(creators, " "), strings.Join(tags, " • "))
	return err
}

// SearchWorkIDs runs an FTS5 MATCH query and returns work ids by rank.
func (s *Store) SearchWorkIDs(ctx context.Context, match string, limit int) ([]int64, error) {
	rows, err := s.Meta.QueryContext(ctx, `SELECT rowid FROM works_fts WHERE works_fts MATCH ? ORDER BY rank LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetWorkStatus records a verification result (deleted/hidden/restricted/...). Content is kept.
func (s *Store) SetWorkStatus(ctx context.Context, workID int64, status model.WorkStatus) error {
	now := s.unix()
	res, err := s.Meta.ExecContext(ctx, `UPDATE works SET
		status_changed_at = CASE WHEN status != ? THEN ? ELSE status_changed_at END,
		status = ?, last_checked = ? WHERE id = ?`, string(status), now, string(status), now, workID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Unknown work (e.g. on-demand fetch of a deleted id): record it so we don't retry blindly.
		_, err = s.Meta.ExecContext(ctx, `INSERT INTO works (id, status, status_changed_at, first_seen, last_checked)
			VALUES (?,?,?,?,?)`, workID, string(status), now, now, now)
	}
	s.log.Debug("work status set", "work_id", workID, "status", string(status))
	return err
}

// WorkStatus returns a work's status and whether it exists.
func (s *Store) WorkStatus(ctx context.Context, workID int64) (model.WorkStatus, bool, error) {
	var st model.WorkStatus
	err := s.Meta.QueryRowContext(ctx, `SELECT status FROM works WHERE id = ?`, workID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return st, err == nil, err
}

// WorkNeedsFetch reports whether a work is ok and was never fetched or changed since its last
// fetch (by revised date, chapters or words).
func (s *Store) WorkNeedsFetch(ctx context.Context, workID int64) (bool, error) {
	var need bool
	err := s.Meta.QueryRowContext(ctx, `SELECT status = 'ok' AND (last_fetched = 0 OR fetched_revised != revised_at
		OR fetched_chapters != chapters_posted OR fetched_words != words) FROM works WHERE id = ?`, workID).Scan(&need)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return need, err
}
