package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// masterKeyRetention is how long a retired master key is kept before DeleteRetiredMasterKeys
// deletes it (docs/part3-key-rotation.md "The keyring"): far longer than any request holds a
// keyring snapshot, so rotation needs no locking against requests.
const masterKeyRetention = 7 * 24 * time.Hour

// MasterKeys owns keys.db and the LiveKeyring built from it (docs/part3-key-rotation.md): it
// creates the first master key, rotates the key (re-encrypting users.db) and deletes retired
// keys. It is safe to share; every rotation step runs under one mutex.
type MasterKeys struct {
	keys      *store.Keys
	users     *store.Users
	live      *auth.LiveKeyring
	log       *slog.Logger
	now       func() time.Time
	rotation  time.Duration
	backupDir string

	mu sync.Mutex // serializes rotation and deletion, so keys.db and live agree under it
}

// MasterKeysOptions configure NewMasterKeys.
type MasterKeysOptions struct {
	Keys   *store.Keys
	Users  *store.Users
	Logger *slog.Logger
	Now    func() time.Time
	// Rotation is auth.master_key_rotation: RotateMasterKeyIfDue replaces a key this old; 720h.
	Rotation time.Duration
	// BackupDir is the directory BackupDatabases writes backups into (api.backup_dir under
	// data_dir); empty refuses backups.
	BackupDir string
}

// NewMasterKeys loads the master keys from keys.db, creating the first (active) key when
// keys.db holds none. It then re-encrypts every users.db value not under the active key, which
// finishes a rotation a crash interrupted, and refuses to start when a value is under a key
// keys.db doesn't hold (a users.db restored without its keys.db).
func NewMasterKeys(ctx context.Context, opts MasterKeysOptions) (*MasterKeys, error) {
	if opts.Keys == nil || opts.Users == nil {
		return nil, errors.New("controllers: master keys need keys.db and users.db")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	m := &MasterKeys{keys: opts.Keys, users: opts.Users, log: opts.Logger.With("component", "auth"),
		now: opts.Now, rotation: durationOr(opts.Rotation, 720*time.Hour), backupDir: opts.BackupDir}
	created := int64(0)
	mks, err := store.WriteKeysValue(ctx, m.keys, func(ctx context.Context, tx *store.KeysTx) ([]model.MasterKey, error) {
		mks, err := tx.MasterKeys(ctx)
		if err != nil || len(mks) > 0 {
			return mks, err
		}
		if created, err = tx.InsertMasterKey(ctx, auth.NewMasterKey(), m.now()); err != nil {
			return nil, err
		}
		return tx.MasterKeys(ctx)
	})
	if err != nil {
		return nil, err
	}
	if created != 0 {
		m.event(ctx, slog.LevelInfo, "master_key_created", "key_id", created)
	}
	r, err := keyringFrom(mks)
	if err != nil {
		return nil, err
	}
	if m.live, err = auth.NewLiveKeyring(r); err != nil {
		return nil, err
	}
	n, err := m.reencrypt(ctx, r)
	if errors.Is(err, auth.ErrKeyNotHeld) {
		return nil, fmt.Errorf("controllers: users.db holds a value under a master key keys.db does not have "+
			"(were they restored from different backups?): %w", err)
	}
	if err != nil {
		return nil, err
	}
	if n.total() > 0 {
		m.event(ctx, slog.LevelInfo, "master_key_reencrypted", append([]any{"reason", reasonStartup, "key_id", r.ActiveID()}, n.attrs()...)...)
	}
	return m, nil
}

// Keyring is the live keyring, for the Hasher and the Auth controller.
func (m *MasterKeys) Keyring() *auth.LiveKeyring { return m.live }

func (m *MasterKeys) event(ctx context.Context, level slog.Level, event string, attrs ...any) {
	logx.From(ctx, m.log).Log(ctx, level, "auth event", append([]any{"event", event}, attrs...)...)
}

// RotateMasterKey retires the active master key, starts a new one and re-encrypts users.db
// under it at once (an admin's request). It returns the new key's ID.
func (m *MasterKeys) RotateMasterKey(ctx context.Context) (int64, error) {
	id, _, err := m.rotate(ctx, false)
	return id, err
}

// RotateMasterKeyIfDue rotates the master key once it is auth.master_key_rotation old. serve
// calls it at startup and then hourly. It is false when the key isn't due yet.
func (m *MasterKeys) RotateMasterKeyIfDue(ctx context.Context) (bool, error) {
	_, rotated, err := m.rotate(ctx, true)
	return rotated, err
}

// rotate runs rotation steps 1-3 (docs/part3-key-rotation.md "Rotation"): in keys.db retire
// the active key and add a new one, swap in the keyring holding both, then re-encrypt users.db.
// A crash between the steps leaves every value under a key still held; the next start re-runs
// step 3. With ifDue it first checks, under the mutex, that the active key is due.
func (m *MasterKeys) rotate(ctx context.Context, ifDue bool) (id int64, rotated bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var oldID int64
	mks, err := store.WriteKeysValue(ctx, m.keys, func(ctx context.Context, tx *store.KeysTx) ([]model.MasterKey, error) {
		mks, err := tx.MasterKeys(ctx)
		if err != nil {
			return nil, err
		}
		i := activeMasterKey(mks)
		if i < 0 {
			return nil, errors.New("controllers: keys.db has no active master key")
		}
		if ifDue && now.Before(mks[i].CreatedAt.Add(m.rotation)) {
			return nil, nil
		}
		oldID = mks[i].ID
		if err := tx.RetireMasterKey(ctx, oldID, now); err != nil {
			return nil, err
		}
		if id, err = tx.InsertMasterKey(ctx, auth.NewMasterKey(), now); err != nil {
			return nil, err
		}
		return tx.MasterKeys(ctx)
	})
	if err != nil || mks == nil {
		return 0, false, err
	}
	r, err := keyringFrom(mks)
	if err != nil {
		return 0, false, err
	}
	m.live.Store(r)
	n, err := m.reencrypt(ctx, r)
	if err != nil {
		return id, true, fmt.Errorf("controllers: master key %d is active but users.db was not re-encrypted "+
			"(the next start, or the deletion of key %d, retries): %w", id, oldID, err)
	}
	reason := reasonAdmin
	if ifDue {
		reason = reasonScheduled
	}
	m.event(ctx, slog.LevelInfo, "master_key_rotated",
		append([]any{"reason", reason, "old_key_id", oldID, "key_id", id}, n.attrs()...)...)
	return id, true, nil
}

// DeleteRetiredMasterKeys deletes every master key retired at least masterKeyRetention ago
// (rotation steps 4-5): it re-encrypts users.db values not under the active key first (normally
// none: any found are logged as stragglers), then deletes the keys and swaps in a keyring
// without them. serve calls it at startup and then hourly. It returns how many it deleted.
func (m *MasterKeys) DeleteRetiredMasterKeys(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	mks, err := store.ReadKeysValue(ctx, m.keys, func(ctx context.Context, tx *store.KeysReadTx) ([]model.MasterKey, error) {
		return tx.MasterKeys(ctx)
	})
	if err != nil {
		return 0, err
	}
	var due []int64
	for _, mk := range mks {
		if !mk.RetiredAt.IsZero() && !now.Before(mk.RetiredAt.Add(masterKeyRetention)) {
			due = append(due, mk.ID)
		}
	}
	if len(due) == 0 {
		return 0, nil
	}
	n, err := m.reencrypt(ctx, m.live.Load())
	if err != nil {
		return 0, err
	}
	if n.total() > 0 {
		m.event(ctx, slog.LevelWarn, "master_key_straggler", append([]any{"key_ids", due}, n.attrs()...)...)
	}
	mks, err = store.WriteKeysValue(ctx, m.keys, func(ctx context.Context, tx *store.KeysTx) ([]model.MasterKey, error) {
		for _, id := range due {
			if err := tx.DeleteMasterKey(ctx, id); err != nil {
				return nil, fmt.Errorf("controllers: delete master key %d: %w", id, err)
			}
		}
		return tx.MasterKeys(ctx)
	})
	if err != nil {
		return 0, err
	}
	r, err := keyringFrom(mks)
	if err != nil {
		return 0, err
	}
	m.live.Store(r)
	for _, id := range due {
		m.event(ctx, slog.LevelInfo, "master_key_deleted", "key_id", id)
	}
	return len(due), nil
}

// reencrypted counts the users.db rows a re-encrypt pass rewrote, per table.
type reencrypted struct{ users, enrollments, recoveryCodes, signingKeys int }

func (n reencrypted) total() int { return n.users + n.enrollments + n.recoveryCodes + n.signingKeys }

func (n reencrypted) attrs() []any {
	return []any{"users", n.users, "enrollments", n.enrollments, "recovery_codes", n.recoveryCodes,
		"signing_keys", n.signingKeys}
}

// reencrypt re-encrypts, in one users.db transaction, every value not under r's active key.
// It is pure decrypt and encrypt work (no Argon2id). A value under a key r doesn't hold fails
// the whole pass with auth.ErrKeyNotHeld.
func (m *MasterKeys) reencrypt(ctx context.Context, r *auth.Keyring) (reencrypted, error) {
	return store.WriteUsersValue(ctx, m.users, func(ctx context.Context, tx *store.UsersTx) (reencrypted, error) {
		return reencryptUsersDB(ctx, tx, r)
	})
}

func reencryptUsersDB(ctx context.Context, tx *store.UsersTx, r *auth.Keyring) (n reencrypted, err error) {
	users, err := tx.ListUsers(ctx)
	if err != nil {
		return n, err
	}
	for _, u := range users {
		phc, c1, err := reencryptPHC(r, auth.UserPassword(u.ID), u.PasswordPHC)
		if err != nil {
			return n, fmt.Errorf("controllers: user %d password: %w", u.ID, err)
		}
		totp, c2, err := reencryptTOTP(r, auth.UserTOTP(u.ID), u.TOTPSecretEnc)
		if err != nil {
			return n, fmt.Errorf("controllers: user %d totp: %w", u.ID, err)
		}
		if c1 || c2 {
			if err := tx.SetUserSecrets(ctx, u.ID, phc, totp); err != nil {
				return n, err
			}
			n.users++
		}
	}
	enrollments, err := tx.Enrollments(ctx)
	if err != nil {
		return n, err
	}
	for _, e := range enrollments {
		phc, c1, err := reencryptPHC(r, auth.EnrollmentPassword(e.InviteID), e.PasswordPHC)
		if err != nil {
			return n, fmt.Errorf("controllers: enrollment %d password: %w", e.InviteID, err)
		}
		totp, c2, err := reencryptTOTP(r, auth.EnrollmentTOTP(e.InviteID), e.TOTPSecretEnc)
		if err != nil {
			return n, fmt.Errorf("controllers: enrollment %d totp: %w", e.InviteID, err)
		}
		if c1 || c2 {
			if err := tx.SetEnrollmentSecrets(ctx, e.InviteID, phc, totp); err != nil {
				return n, err
			}
			n.enrollments++
		}
	}
	codes, err := tx.RecoveryCodes(ctx)
	if err != nil {
		return n, err
	}
	for _, c := range codes {
		enc, changed, err := r.ReencryptRecoveryCode(c.UserID, c.CodeEnc)
		if err != nil {
			return n, fmt.Errorf("controllers: recovery code %d: %w", c.ID, err)
		}
		if changed {
			if err := tx.SetRecoveryCodeEnc(ctx, c.ID, enc); err != nil {
				return n, err
			}
			n.recoveryCodes++
		}
	}
	keys, err := tx.SigningKeys(ctx)
	if err != nil {
		return n, err
	}
	for i, k := range keys {
		enc, changed, err := r.ReencryptSigningSeed(k.ID, k.SeedEnc)
		if err != nil { // the kid is a random public value, but logs name rows by number
			return n, fmt.Errorf("controllers: signing key %d of %d: %w", i+1, len(keys), err)
		}
		if changed {
			if err := tx.SetSigningKeySeed(ctx, k.ID, enc); err != nil {
				return n, err
			}
			n.signingKeys++
		}
	}
	return n, nil
}

// reencryptPHC is Keyring.ReencryptPasswordHash, keeping "" (an enrollment that keeps the
// current password) as it is.
func reencryptPHC(r *auth.Keyring, c auth.PasswordContext, phc string) (string, bool, error) {
	if phc == "" {
		return "", false, nil
	}
	return r.ReencryptPasswordHash(c, phc)
}

// reencryptTOTP is Keyring.ReencryptTOTPSecret, keeping nil (an enrollment that keeps the
// current TOTP) as it is.
func reencryptTOTP(r *auth.Keyring, c auth.TOTPContext, enc []byte) ([]byte, bool, error) {
	if enc == nil {
		return nil, false, nil
	}
	return r.ReencryptTOTPSecret(c, enc)
}

// activeMasterKey is the index of the active key in keys.db's rows, or -1.
func activeMasterKey(mks []model.MasterKey) int {
	for i, mk := range mks {
		if mk.RetiredAt.IsZero() {
			return i
		}
	}
	return -1
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
