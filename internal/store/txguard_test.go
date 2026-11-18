package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func noop(context.Context, *Tx) error { return nil }

func noopRead(context.Context, *ReadTx) error { return nil }

func TestGuardNestedTransaction(t *testing.T) {
	s, _, _ := openTest(t)
	err := s.st.InTx(context.Background(), func(ctx context.Context, _ *Tx) error {
		return s.st.InReadTx(ctx, noopRead)
	})
	require.ErrorIs(t, err, ErrTxMisuse)
	require.ErrorContains(t, err, "nested")
}

func TestGuardFetchInsideTransaction(t *testing.T) {
	s, _, _ := openTest(t)
	err := s.st.InTx(context.Background(), func(ctx context.Context, _ *Tx) error {
		return NotInTx(ctx)
	})
	require.ErrorIs(t, err, ErrTxMisuse)
	require.ErrorContains(t, err, "fetch inside")
}

func TestGuardAllowsSequentialTransactions(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.st.InReadTx(ctx, noopRead))
	require.NoError(t, NotInTx(ctx), "the caller's ctx is never marked: only the callback's is")
	require.NoError(t, s.st.InTx(ctx, noop))
	require.NoError(t, s.st.InTx(ctx, noop))
}
