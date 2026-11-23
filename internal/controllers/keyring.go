package controllers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// LoadKeyring loads the master keys from keys.db into a Keyring, creating the first (active)
// key when keys.db holds none (docs/part3-key-rotation.md). now is the new key's created_at.
func LoadKeyring(ctx context.Context, keys *store.Keys, now time.Time, log *slog.Logger) (*auth.Keyring, error) {
	created := int64(0)
	mks, err := store.WriteKeysValue(ctx, keys, func(ctx context.Context, tx *store.KeysTx) ([]model.MasterKey, error) {
		mks, err := tx.MasterKeys(ctx)
		if err != nil || len(mks) > 0 {
			return mks, err
		}
		if created, err = tx.InsertMasterKey(ctx, auth.NewMasterKey(), now); err != nil {
			return nil, err
		}
		return tx.MasterKeys(ctx)
	})
	if err != nil {
		return nil, err
	}
	if created != 0 {
		logx.From(ctx, log).Info("auth event", "component", "auth", "event", "master_key_created", "key_id", created)
	}
	return keyringFrom(mks)
}

// keyringFrom builds a Keyring from keys.db's rows: exactly one must be active.
func keyringFrom(mks []model.MasterKey) (*auth.Keyring, error) {
	active := int64(0)
	ring := make([]auth.MasterKey, len(mks))
	for i, mk := range mks {
		if mk.RetiredAt.IsZero() {
			if active != 0 {
				return nil, fmt.Errorf("controllers: keys.db has two active master keys (%d and %d)", active, mk.ID)
			}
			active = mk.ID
		}
		ring[i] = auth.MasterKey{ID: mk.ID, Key: mk.Key}
	}
	if active == 0 {
		return nil, fmt.Errorf("controllers: keys.db has no active master key (%d keys)", len(mks))
	}
	return auth.NewKeyring(active, ring)
}
