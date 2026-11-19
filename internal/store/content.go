package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// ErrNoRawPage means no raw page is stored for a work.
var ErrNoRawPage = errors.New("store: no raw page")

// PutRawPage stores a fetched work page (zstd) unless an identical copy exists, then prunes
// older versions beyond keep. The newest copy is never pruned.
// It returns whether the page was new (not a duplicate of a stored version).
func (s *Tx) PutRawPage(ctx context.Context, workID int64, path string, page []byte, keep int) (bool, error) {
	return s.putRawPage(ctx, workID, path, page, keep, false)
}

// PutFailedRawPage stores a fetched page that did not parse, for `reparse` after a parser fix.
// It never pushes out a good version: only the newest failed page is kept, and only while it
// is newer than every good one. keep is the number of good versions to keep.
func (s *Tx) PutFailedRawPage(ctx context.Context, workID int64, path string, page []byte, keep int) (bool, error) {
	return s.putRawPage(ctx, workID, path, page, keep, true)
}

func (s *Tx) putRawPage(ctx context.Context, workID int64, path string, page []byte, keep int, failed bool) (bool, error) {
	start := time.Now()
	sum := sha256.Sum256(page)
	hash := hex.EncodeToString(sum[:])
	z := s.compress(page)
	defer s.timed("put_raw_page", start, "work_id", workID, "bytes", len(page))
	tx := s.content
	var existing int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM raw_pages WHERE work_id = ? AND sha256 = ?`, workID, hash).Scan(&existing)
	isNew := errors.Is(err, sql.ErrNoRows)
	switch {
	case isNew:
		_, err = tx.ExecContext(ctx, `INSERT INTO raw_pages (work_id, fetched_at, path, sha256, size, html_zstd, parse_failed)
			VALUES (?,?,?,?,?,?,?)`, workID, s.unix(), path, hash, len(page), z, failed)
	case err == nil:
		_, err = tx.ExecContext(ctx, `UPDATE raw_pages SET fetched_at = ?, parse_failed = ? WHERE id = ?`, s.unix(), failed, existing)
	}
	if err != nil {
		return false, err
	}
	var versions int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM raw_pages WHERE work_id = ?`, workID).Scan(&versions); err != nil {
		return false, err
	}
	if keep < 1 {
		keep = 1
	}
	// Keep the newest `keep` good pages, plus the newest failed page if nothing good came after it.
	pruned, err := tx.ExecContext(ctx, `DELETE FROM raw_pages WHERE work_id = ?1
		AND id NOT IN (SELECT id FROM raw_pages WHERE work_id = ?1 AND parse_failed = 0
			ORDER BY fetched_at DESC, id DESC LIMIT ?2)
		AND id NOT IN (SELECT f.id FROM raw_pages f WHERE f.work_id = ?1 AND f.parse_failed = 1
			AND NOT EXISTS (SELECT 1 FROM raw_pages g WHERE g.work_id = ?1 AND g.parse_failed = 0
				AND (g.fetched_at > f.fetched_at OR (g.fetched_at = f.fetched_at AND g.id > f.id)))
			ORDER BY f.fetched_at DESC, f.id DESC LIMIT 1)`, workID, keep)
	if err != nil {
		return false, err
	}
	n, _ := pruned.RowsAffected()
	s.log.Debug("raw page stored", "work_id", workID, "bytes", len(page), "zstd_bytes", len(z),
		"duplicate", !isNew, "parse_failed", failed, "versions_before_prune", versions, "pruned", n)
	return isNew, nil
}

// LatestRawPage returns the newest stored page for a work.
func (s *ReadTx) LatestRawPage(ctx context.Context, workID int64) ([]byte, time.Time, error) {
	var z []byte
	var at int64
	err := s.content.QueryRowContext(ctx, `SELECT html_zstd, fetched_at FROM raw_pages WHERE work_id = ?
		ORDER BY fetched_at DESC, id DESC LIMIT 1`, workID).Scan(&z, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNoRawPage
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	b, err := s.decompress(z)
	return b, time.Unix(at, 0), err
}

// RawPageVersion identifies a stored raw page version without loading it.
type RawPageVersion struct {
	ID        int64
	FetchedAt int64 // unix seconds; a re-stored identical page moves it
}

// LatestRawPageVersion returns which raw page version is newest for a work, without reading or
// decompressing the page (cheap enough for a check inside a write transaction).
func (s *ReadTx) LatestRawPageVersion(ctx context.Context, workID int64) (RawPageVersion, error) {
	var v RawPageVersion
	err := s.content.QueryRowContext(ctx, `SELECT id, fetched_at FROM raw_pages WHERE work_id = ?
		ORDER BY fetched_at DESC, id DESC LIMIT 1`, workID).Scan(&v.ID, &v.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNoRawPage
	}
	return v, err
}

// RawPageWorkIDs pages through work ids that have raw pages (for reparse), ascending after afterID.
func (s *ReadTx) RawPageWorkIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	return queryInt64s(ctx, s.content, `SELECT DISTINCT work_id FROM raw_pages WHERE work_id > ?
		ORDER BY work_id LIMIT ?`, afterID, limit)
}

// PutWorkContent replaces a work's chapter bodies and skin.
func (s *Tx) PutWorkContent(ctx context.Context, workID int64, chs []model.Chapter, skinCSS string) (ContentChange, error) {
	start := time.Now()
	defer s.timed("put_work_content", start, "work_id", workID, "chapters", len(chs))
	change, err := s.compareWorkContent(ctx, workID, chs, skinCSS)
	if err != nil {
		return "", err
	}
	tx := s.content
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapter_bodies WHERE work_id = ?`, workID); err != nil {
		return "", err
	}
	total := 0
	for _, c := range chs {
		body := s.compress([]byte(c.BodyHTML))
		if body == nil {
			body = []byte{}
		}
		total += len(c.BodyHTML)
		if _, err := tx.ExecContext(ctx, `INSERT INTO chapter_bodies (work_id, position, chapter_id, body_zstd, notes_zstd, endnotes_zstd, summary_zstd)
			VALUES (?,?,?,?,?,?,?)`, workID, c.Position, c.ID, body,
			s.compress([]byte(c.NotesHTML)), s.compress([]byte(c.EndNotesHTML)), s.compress([]byte(c.SummaryHTML))); err != nil {
			return "", err
		}
	}
	if skinCSS == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM work_skins WHERE work_id = ?`, workID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO work_skins (work_id, css) VALUES (?,?)
			ON CONFLICT (work_id) DO UPDATE SET css = excluded.css`, workID, skinCSS)
	}
	if err != nil {
		return "", err
	}
	s.log.Debug("work content stored", "work_id", workID, "chapters", len(chs), "body_bytes", total,
		"skin_bytes", len(skinCSS), "content", string(change))
	return change, nil
}

// ContentChange says how a work's stored content (chapter bodies, notes, summaries, skin)
// compares with what was stored before. Unlike the raw page, which differs on nearly every
// fetch (hit counts, CSRF tokens, Cloudflare's injected script), this only changes when the
// work itself does (or the parser extracts it differently).
type ContentChange string

const (
	ContentNew       ContentChange = "new"       // nothing stored before
	ContentChanged   ContentChange = "changed"   // chapters, text, notes or skin differ
	ContentUnchanged ContentChange = "unchanged" // identical to what was stored
)

// ContentChanges lists every ContentChange.
func ContentChanges() []ContentChange {
	return []ContentChange{ContentNew, ContentChanged, ContentUnchanged}
}

// compareWorkContent compares new content with the stored copy. Content is compared, never
// logged.
func (s *ReadTx) compareWorkContent(ctx context.Context, workID int64, chs []model.Chapter, skinCSS string) (ContentChange, error) {
	old, err := s.ChapterBodies(ctx, workID)
	if err != nil {
		return "", err
	}
	if len(old) == 0 {
		return ContentNew, nil
	}
	var oldSkin string
	if err := s.content.QueryRowContext(ctx, `SELECT css FROM work_skins WHERE work_id = ?`, workID).Scan(&oldSkin); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if oldSkin != skinCSS || len(old) != len(chs) {
		return ContentChanged, nil
	}
	for i, c := range chs {
		o := old[i]
		if o.Position != c.Position || o.ID != c.ID || o.BodyHTML != c.BodyHTML || o.NotesHTML != c.NotesHTML ||
			o.EndNotesHTML != c.EndNotesHTML || o.SummaryHTML != c.SummaryHTML {
			return ContentChanged, nil
		}
	}
	return ContentUnchanged, nil
}

// ChapterBodies returns stored chapter bodies in order (for the API/UI; never log them).
func (s *ReadTx) ChapterBodies(ctx context.Context, workID int64) ([]model.Chapter, error) {
	rows, err := s.content.QueryContext(ctx, `SELECT position, chapter_id, body_zstd, notes_zstd, endnotes_zstd, summary_zstd
		FROM chapter_bodies WHERE work_id = ? ORDER BY position`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Chapter
	for rows.Next() {
		var c model.Chapter
		var body, notes, end, summary []byte
		if err := rows.Scan(&c.Position, &c.ID, &body, &notes, &end, &summary); err != nil {
			return nil, err
		}
		for _, p := range []struct {
			z   []byte
			dst *string
		}{{body, &c.BodyHTML}, {notes, &c.NotesHTML}, {end, &c.EndNotesHTML}, {summary, &c.SummaryHTML}} {
			b, err := s.decompress(p.z)
			if err != nil {
				return nil, err
			}
			*p.dst = string(b)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
