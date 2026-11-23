package controllers

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestLoadKeyring(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "api")
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))

	keys, err := store.OpenKeys(ctx, dir, store.Options{})
	require.NoError(t, err)
	l1, err := LoadKeyring(ctx, keys, t0, log)
	require.NoError(t, err)
	r1 := l1.Load()
	require.EqualValues(t, 1, r1.ActiveID())
	require.Equal(t, 1, strings.Count(logs.String(), `"event":"master_key_created","key_id":1`))
	stored, err := store.ReadKeysValue(ctx, keys, func(ctx context.Context, tx *store.KeysReadTx) ([]model.MasterKey, error) {
		return tx.MasterKeys(ctx)
	})
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.True(t, stored[0].CreatedAt.Equal(t0))
	require.NotContains(t, logs.String(), string(stored[0].Key), "no key material in logs")
	require.NoError(t, keys.Close())

	// A restart loads the same key: what r1 encrypted still decrypts.
	enc := r1.EncryptTOTPSecret(auth.UserTOTP(1), []byte("fixture secret"))
	keys, err = store.OpenKeys(ctx, dir, store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keys.Close()) })
	l2, err := LoadKeyring(ctx, keys, t0.Add(time.Hour), log)
	require.NoError(t, err)
	r2 := l2.Load()
	require.EqualValues(t, 1, r2.ActiveID())
	got, err := r2.DecryptTOTPSecret(auth.UserTOTP(1), enc)
	require.NoError(t, err)
	require.Equal(t, []byte("fixture secret"), got)
	require.Equal(t, 1, strings.Count(logs.String(), "master_key_created"), "no second key")
}

func TestKeyringFrom(t *testing.T) {
	k := func(id int64, retired bool) model.MasterKey {
		mk := model.MasterKey{ID: id, Key: auth.NewMasterKey()}
		if retired {
			mk.RetiredAt = time.Unix(1, 0)
		}
		return mk
	}
	r, err := keyringFrom([]model.MasterKey{k(1, true), k(2, false)})
	require.NoError(t, err)
	require.EqualValues(t, 2, r.ActiveID())

	_, err = keyringFrom(nil)
	require.ErrorContains(t, err, "no active master key")
	_, err = keyringFrom([]model.MasterKey{k(1, true)})
	require.ErrorContains(t, err, "no active master key")
	_, err = keyringFrom([]model.MasterKey{k(1, false), k(2, false)})
	require.ErrorContains(t, err, "two active")
}
