package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func countRows(t *testing.T, s *testStore) (works, rawPages int) {
	t.Helper()
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&works))
	require.NoError(t, s.content.QueryRow(`SELECT COUNT(*) FROM raw_pages`).Scan(&rawPages))
	return works, rawPages
}

func TestInTxCommitsBothDatabases(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	_, err := s.st.InTx(ctx, func(ctx context.Context, tx *Tx) error {
		if _, err := tx.PutRawPage(ctx, 1, "/works/1", []byte("<p>fixture</p>"), 1); err != nil {
			return err
		}
		_, err := tx.UpsertWork(ctx, fixtureWork(1), 0, 1)
		return err
	})
	require.NoError(t, err)
	works, raw := countRows(t, s)
	require.Equal(t, 1, works)
	require.Equal(t, 1, raw)
}

func TestInTxRollsBackOnError(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	boom := errors.New("fixture failure")
	_, err := s.st.InTx(ctx, func(ctx context.Context, tx *Tx) error {
		if _, err := tx.PutRawPage(ctx, 1, "/works/1", []byte("<p>fixture</p>"), 1); err != nil {
			return err
		}
		if _, err := tx.UpsertWork(ctx, fixtureWork(1), 0, 1); err != nil {
			return err
		}
		return boom
	})
	require.ErrorIs(t, err, boom)
	works, raw := countRows(t, s)
	require.Zero(t, works, "meta write must roll back")
	require.Zero(t, raw, "content write must roll back")
}

func TestInTxRollsBackOnPanic(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	require.PanicsWithValue(t, "fixture panic", func() {
		_, _ = s.st.InTx(ctx, func(ctx context.Context, tx *Tx) error {
			if _, err := tx.UpsertWork(ctx, fixtureWork(1), 0, 1); err != nil {
				return err
			}
			panic("fixture panic")
		})
	})
	works, _ := countRows(t, s)
	require.Zero(t, works, "panic must roll back")
	// The locks were released: a new write transaction works.
	_, err := s.st.InTx(ctx, func(ctx context.Context, tx *Tx) error {
		_, err := tx.UpsertWork(ctx, fixtureWork(2), 0, 1)
		return err
	})
	require.NoError(t, err)
}

func TestReadTxDoesNotBlockWriters(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	inRead, release := make(chan struct{}), make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		_, err := s.st.InReadTx(ctx, func(ctx context.Context, tx *ReadTx) error {
			if _, err := tx.Counts(ctx); err != nil { // take the read snapshot
				return err
			}
			close(inRead)
			<-release
			return nil
		})
		readDone <- err
	}()
	<-inRead
	start := time.Now()
	_, err := s.st.InTx(ctx, func(ctx context.Context, tx *Tx) error {
		_, err := tx.UpsertWork(ctx, fixtureWork(1), 0, 1)
		return err
	})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "writer waited on an open read transaction")
	close(release)
	require.NoError(t, <-readDone)
}

func TestValueHelpers(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	_, res, err := WriteValue(ctx, s.st, func(ctx context.Context, tx *Tx) (UpsertResult, error) {
		return tx.UpsertWork(ctx, fixtureWork(1), 0, 1)
	})
	require.NoError(t, err)
	require.True(t, res.New)
	_, ok, err := ReadValue(ctx, s.st, func(ctx context.Context, tx *ReadTx) (bool, error) {
		return tx.WorkExists(ctx, 1)
	})
	require.NoError(t, err)
	require.True(t, ok)
}
