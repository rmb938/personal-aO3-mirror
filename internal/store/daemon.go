package store

import (
	"context"
	"time"
)

// SetDaemonState upserts the daemon's content-free heartbeat keys (read by `status`).
func (s *Store) SetDaemonState(ctx context.Context, kv map[string]string) error {
	tx, err := s.meta.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	now := s.unix()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO daemon_state (key, value, updated_at) VALUES (?,?,?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, k, v, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DaemonState returns the daemon's heartbeat keys and when they were last written.
func (s *Store) DaemonState(ctx context.Context) (map[string]string, time.Time, error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT key, value, updated_at FROM daemon_state`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	out := map[string]string{}
	var latest int64
	for rows.Next() {
		var k, v string
		var at int64
		if err := rows.Scan(&k, &v, &at); err != nil {
			return nil, time.Time{}, err
		}
		out[k] = v
		if at > latest {
			latest = at
		}
	}
	return out, unixTime(latest), rows.Err()
}
