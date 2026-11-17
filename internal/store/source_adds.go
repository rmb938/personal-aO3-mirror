package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoSourceAdd means a queued source add no longer exists (done, failed or never queued).
var ErrNoSourceAdd = errors.New("store: no such queued source add")

// SourceAdd is a listing source waiting for the daemon to fetch its page 1. URL may hold
// tag/user names: never log it.
type SourceAdd struct {
	ID           int64
	URL          string
	Order        string        // "" = default
	Priority     int           // higher is picked first within a class
	PollInterval time.Duration // 0 = default
	Confirm      bool
}

// InsertSourceAdd queues a source add and returns its id.
func (s *Tx) InsertSourceAdd(ctx context.Context, a SourceAdd) (int64, error) {
	res, err := s.meta.ExecContext(ctx, `INSERT INTO source_adds (url, backfill_order, priority, poll_interval_s, confirm, created_at)
		VALUES (?,?,?,?,?,?)`, a.URL, a.Order, a.Priority, int64(a.PollInterval/time.Second), a.Confirm, s.unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetSourceAdd returns a queued source add.
func (s *ReadTx) GetSourceAdd(ctx context.Context, id int64) (SourceAdd, error) {
	a := SourceAdd{ID: id}
	var poll int64
	err := s.meta.QueryRowContext(ctx, `SELECT url, backfill_order, priority, poll_interval_s, confirm FROM source_adds WHERE id = ?`, id).
		Scan(&a.URL, &a.Order, &a.Priority, &poll, &a.Confirm)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNoSourceAdd
	}
	a.PollInterval = time.Duration(poll) * time.Second
	return a, err
}

// DeleteSourceAdd removes a queued source add (a missing row is not an error).
func (s *Tx) DeleteSourceAdd(ctx context.Context, id int64) error {
	_, err := s.meta.ExecContext(ctx, `DELETE FROM source_adds WHERE id = ?`, id)
	return err
}
