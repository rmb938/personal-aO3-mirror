package controllers

import (
	"context"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// History retention: finished jobs (and their events) and request rows are kept this long.
const (
	keepDoneJobs   = 7 * 24 * time.Hour  // done and cancelled jobs
	keepFailedJobs = 30 * 24 * time.Hour // failed jobs stay longer, for debugging
	keepRequests   = 30 * 24 * time.Hour
)

// PruneResult counts what PruneHistory deleted.
type PruneResult struct {
	Jobs     int64
	Requests int64
}

// PruneHistory deletes old finished jobs and request rows (one transaction, no requests).
// Mirrored content is never pruned.
func (c *Controllers) PruneHistory(ctx context.Context) (PruneResult, error) {
	var res PruneResult
	err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		if res.Jobs, err = tx.PruneJobs(ctx, keepDoneJobs, keepFailedJobs); err != nil {
			return err
		}
		res.Requests, err = tx.PruneRequests(ctx, keepRequests)
		return err
	})
	return res, err
}
