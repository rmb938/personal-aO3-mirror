package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
)

// Image statuses (images.status CHECK).
const (
	ImageQueued     = "queued"
	ImageOK         = "ok"
	ImageFailed     = "failed"
	ImageSkipped    = "skipped"
	ImageTooLarge   = "too_large"
	ImageDisallowed = "disallowed"
)

// ImageStatuses lists every image status.
func ImageStatuses() []string {
	return []string{ImageQueued, ImageOK, ImageFailed, ImageSkipped, ImageTooLarge, ImageDisallowed}
}

// ImageRow is one queued image. URL may contain names: never log it (log ID and hashed host).
type ImageRow struct {
	ID       int64
	URL      string
	Host     string
	Attempts int
}

// ImageHost is the per-host key for image pacing: the hostname, lower-cased, without a
// fully-qualified trailing dot ("Example.com." and "example.com" are one host). The image
// worker keys its intervals, backoffs and robots cache by it too.
func ImageHost(u *url.URL) string {
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// QueueImages records the images a work embeds and queues new ones for download.
func (s *Tx) QueueImages(ctx context.Context, workID int64, urls []string) (int, error) {
	if len(urls) == 0 {
		return 0, nil
	}
	tx := s.meta
	now := s.unix()
	added := 0
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO images (url, host, status, first_seen) VALUES (?,?, 'queued', ?)
			ON CONFLICT (url) DO NOTHING`, raw, ImageHost(u), now)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			added++
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_images (work_id, image_id)
			SELECT ?, id FROM images WHERE url = ?`, workID, raw); err != nil {
			return added, err
		}
	}
	return added, nil
}

// NextImages returns due queued images, oldest first, leaving out skipHosts (hosts the image
// worker is holding back) so they never crowd out other hosts' images.
func (s *ReadTx) NextImages(ctx context.Context, now time.Time, limit int, skipHosts []string) ([]ImageRow, error) {
	q := `SELECT id, url, host, attempts FROM images WHERE status = 'queued' AND not_before <= ?`
	args := []any{now.Unix()}
	if len(skipHosts) > 0 {
		q += ` AND host NOT IN (` + placeholders(len(skipHosts)) + `)` // only "?,?,..." is concatenated
		for _, h := range skipHosts {
			args = append(args, h)
		}
	}
	rows, err := s.meta.QueryContext(ctx, q+` ORDER BY not_before, id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageRow
	for rows.Next() {
		var r ImageRow
		if err := rows.Scan(&r.ID, &r.URL, &r.Host, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NextImageDueForTestOnly returns when the earliest queued image becomes due (zero if none
// queued). Only the imagefetch tests use it, to advance their fake clock; production code must
// not call it (the image worker paces itself from NextImages).
func (s *ReadTx) NextImageDueForTestOnly(ctx context.Context) (time.Time, error) {
	var nb int64
	err := s.meta.QueryRowContext(ctx, `SELECT COALESCE(MIN(not_before), -1) FROM images WHERE status = 'queued'`).Scan(&nb)
	if err != nil || nb < 0 {
		return time.Time{}, err
	}
	return time.Unix(nb, 0), nil
}

func (s *Tx) ImageDone(ctx context.Context, id int64, sha, contentType string, bytes int) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE images SET status = 'ok', sha256 = ?, content_type = ?, bytes = ?, fetched_at = ?,
		last_error = '', attempts = attempts + 1 WHERE id = ?`, sha, contentType, bytes, s.unix(), id)
	return err
}

// ImageRetry re-queues after notBefore; after maxAttempts the image is marked failed.
func (s *Tx) ImageRetry(ctx context.Context, id int64, notBefore time.Time, lastError string, maxAttempts int) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE images SET attempts = attempts + 1, last_error = ?, not_before = ?,
		status = CASE WHEN attempts + 1 >= ? THEN 'failed' ELSE 'queued' END WHERE id = ?`, lastError, notBefore.Unix(), maxAttempts, id)
	return err
}

// ImageDefer puts an image off until notBefore without counting an attempt: nothing was sent
// for it (its host was held back, or its robots.txt could not be read).
func (s *Tx) ImageDefer(ctx context.Context, id int64, notBefore time.Time, lastError string) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE images SET last_error = ?, not_before = ? WHERE id = ? AND status = 'queued'`,
		lastError, notBefore.Unix(), id)
	return err
}

// DueImageHosts returns which of hosts have queued images due at now.
func (s *ReadTx) DueImageHosts(ctx context.Context, now time.Time, hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	args := []any{now.Unix()}
	for _, h := range hosts {
		args = append(args, h)
	}
	rows, err := s.meta.QueryContext(ctx, `SELECT DISTINCT host FROM images WHERE status = 'queued' AND not_before <= ?
		AND host IN (`+placeholders(len(hosts))+`)`, args...) // only "?,?,..." is concatenated
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ImageFinal marks an image permanently failed/skipped/too_large/disallowed.
func (s *Tx) ImageFinal(ctx context.Context, id int64, status, lastError string) error {
	_, err := s.meta.ExecContext(ctx, `UPDATE images SET status = ?, last_error = ?, attempts = attempts + 1, fetched_at = ?
		WHERE id = ?`, status, lastError, s.unix(), id)
	return err
}

// ErrNoImage means no downloaded (ok) image has the requested sha256.
var ErrNoImage = errors.New("store: no such image")

// StoredImage is a downloaded image's file: its hash, the Content-Type the host served it with
// (imagefetch keeps image/* only) and its size. Content-free.
type StoredImage struct {
	ID          int64 // the images row read (the first stored with this hash)
	SHA256      string
	ContentType string
	Bytes       int
}

// imageBySHA reads one downloaded image by hash through the images_sha256 partial index (its
// WHERE must keep status = 'ok' for SQLite to use it). Rows with equal bytes from different
// URLs share a hash; the first one stored wins, so the answer is stable.
const imageBySHA = `SELECT id, content_type, bytes FROM images WHERE sha256 = ? AND status = 'ok' ORDER BY id LIMIT 1`

// ImageBySHA returns the downloaded image with hash sha, or ErrNoImage.
func (s *ReadTx) ImageBySHA(ctx context.Context, sha string) (StoredImage, error) {
	img := StoredImage{SHA256: sha}
	err := s.meta.QueryRowContext(ctx, imageBySHA, sha).Scan(&img.ID, &img.ContentType, &img.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return img, ErrNoImage
	}
	return img, err
}

// WorkImage is one image a work embeds. URL is the <img src> as stored (it can hold names:
// never log it); SHA256 is set once the image is downloaded (Status ok), else "".
type WorkImage struct {
	URL    string
	Status string
	SHA256 string
}

// workImages reads a work's images by its work_images primary key, in the order they were
// first seen (image id).
const workImages = `SELECT i.url, i.status, i.sha256 FROM work_images wi JOIN images i ON i.id = wi.image_id
	WHERE wi.work_id = ? ORDER BY i.id`

// WorkImages returns the images work workID embeds (none for an unknown work).
func (s *ReadTx) WorkImages(ctx context.Context, workID int64) ([]WorkImage, error) {
	rows, err := s.meta.QueryContext(ctx, workImages, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkImage
	for rows.Next() {
		var w WorkImage
		if err := rows.Scan(&w.URL, &w.Status, &w.SHA256); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ImageCounts groups images by status.
func (s *ReadTx) ImageCounts(ctx context.Context) (map[string]int, int64, error) {
	out := map[string]int{}
	if err := countGroups(ctx, s.meta, `SELECT status, COUNT(*) FROM images GROUP BY status`, out); err != nil {
		return nil, 0, err
	}
	var bytes int64
	err := s.meta.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes), 0) FROM images WHERE status = 'ok'`).Scan(&bytes)
	return out, bytes, err
}
