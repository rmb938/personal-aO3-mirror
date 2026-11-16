package store

import (
	"context"
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

// ImageRow is one queued image. URL may contain names: never log it (log ID and hashed host).
type ImageRow struct {
	ID       int64
	URL      string
	Host     string
	Attempts int
}

// QueueImages records the images a work embeds and queues new ones for download.
func (s *Store) QueueImages(ctx context.Context, workID int64, urls []string) (int, error) {
	if len(urls) == 0 {
		return 0, nil
	}
	tx, err := s.Meta.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	now := s.unix()
	added := 0
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO images (url, host, status, first_seen) VALUES (?,?, 'queued', ?)
			ON CONFLICT (url) DO NOTHING`, raw, strings.ToLower(u.Hostname()), now)
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
	return added, tx.Commit()
}

// NextImages returns due queued images, oldest first.
func (s *Store) NextImages(ctx context.Context, now time.Time, limit int) ([]ImageRow, error) {
	rows, err := s.Meta.QueryContext(ctx, `SELECT id, url, host, attempts FROM images
		WHERE status = 'queued' AND not_before <= ? ORDER BY not_before, id LIMIT ?`, now.Unix(), limit)
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

// NextImageDue returns when the earliest queued image becomes due (zero if none queued).
func (s *Store) NextImageDue(ctx context.Context) (time.Time, error) {
	var nb int64
	err := s.Meta.QueryRowContext(ctx, `SELECT COALESCE(MIN(not_before), -1) FROM images WHERE status = 'queued'`).Scan(&nb)
	if err != nil || nb < 0 {
		return time.Time{}, err
	}
	return time.Unix(nb, 0), nil
}

func (s *Store) ImageDone(ctx context.Context, id int64, sha, contentType string, bytes int) error {
	_, err := s.Meta.ExecContext(ctx, `UPDATE images SET status = 'ok', sha256 = ?, content_type = ?, bytes = ?, fetched_at = ?,
		last_error = '', attempts = attempts + 1 WHERE id = ?`, sha, contentType, bytes, s.unix(), id)
	return err
}

// ImageRetry re-queues after notBefore; after maxAttempts the image is marked failed.
func (s *Store) ImageRetry(ctx context.Context, id int64, notBefore time.Time, lastError string, maxAttempts int) error {
	_, err := s.Meta.ExecContext(ctx, `UPDATE images SET attempts = attempts + 1, last_error = ?, not_before = ?,
		status = CASE WHEN attempts + 1 >= ? THEN 'failed' ELSE 'queued' END WHERE id = ?`, lastError, notBefore.Unix(), maxAttempts, id)
	return err
}

// ImageFinal marks an image permanently failed/skipped/too_large/disallowed.
func (s *Store) ImageFinal(ctx context.Context, id int64, status, lastError string) error {
	_, err := s.Meta.ExecContext(ctx, `UPDATE images SET status = ?, last_error = ?, attempts = attempts + 1, fetched_at = ?
		WHERE id = ?`, status, lastError, s.unix(), id)
	return err
}

// ImageCounts groups images by status.
func (s *Store) ImageCounts(ctx context.Context) (map[string]int, int64, error) {
	rows, err := s.Meta.QueryContext(ctx, `SELECT status, COUNT(*) FROM images GROUP BY status`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, 0, err
		}
		out[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var bytes int64
	err = s.Meta.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes), 0) FROM images WHERE status = 'ok'`).Scan(&bytes)
	return out, bytes, err
}
