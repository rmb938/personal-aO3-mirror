package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestNewMasterKeys(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "api")
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return t0 }
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))

	users, err := store.OpenUsers(ctx, dir, store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, users.Close()) })
	keys, err := store.OpenKeys(ctx, dir, store.Options{})
	require.NoError(t, err)
	m1, err := NewMasterKeys(ctx, MasterKeysOptions{Keys: keys, Users: users, Logger: log, Now: now})
	require.NoError(t, err)
	r1 := m1.Keyring().Load()
	require.EqualValues(t, 1, r1.ActiveID())
	require.Equal(t, 1, strings.Count(logs.String(), `"event":"master_key_created","key_id":1`))
	stored := masterKeys(t, keys)
	require.Len(t, stored, 1)
	require.True(t, stored[0].CreatedAt.Equal(t0))
	require.NotContains(t, logs.String(), string(stored[0].Key), "no key material in logs")
	require.NoError(t, keys.Close())

	// A restart loads the same key: what r1 encrypted still decrypts.
	enc := r1.EncryptTOTPSecret(auth.UserTOTP(1), []byte("fixture secret"))
	keys, err = store.OpenKeys(ctx, dir, store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keys.Close()) })
	m2, err := NewMasterKeys(ctx, MasterKeysOptions{Keys: keys, Users: users, Logger: log, Now: now})
	require.NoError(t, err)
	r2 := m2.Keyring().Load()
	require.EqualValues(t, 1, r2.ActiveID())
	got, err := r2.DecryptTOTPSecret(auth.UserTOTP(1), enc)
	require.NoError(t, err)
	require.Equal(t, []byte("fixture secret"), got)
	require.Equal(t, 1, strings.Count(logs.String(), "master_key_created"), "no second key")
	require.NotContains(t, logs.String(), "master_key_reencrypted", "nothing to finish")

	_, err = NewMasterKeys(ctx, MasterKeysOptions{Keys: keys})
	require.ErrorContains(t, err, "need keys.db and users.db")
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

func masterKeys(t *testing.T, keys *store.Keys) []model.MasterKey {
	t.Helper()
	mks, err := store.ReadKeysValue(context.Background(), keys, func(ctx context.Context, tx *store.KeysReadTx) ([]model.MasterKey, error) {
		return tx.MasterKeys(ctx)
	})
	require.NoError(t, err)
	return mks
}

func masterKeyIDs(t *testing.T, keys *store.Keys) []int64 {
	t.Helper()
	var ids []int64
	for _, mk := range masterKeys(t, keys) {
		ids = append(ids, mk.ID)
	}
	return ids
}

// seedSecrets gives e a value of every kind: a user (password, TOTP, recovery codes), a
// pending signup (password, TOTP) and the signing key NewAuth made. It returns the user.
func seedSecrets(t *testing.T, e *authEnv) testUser {
	t.Helper()
	u := e.signup(t, "fixture_user")
	_, err := e.a.Signup(context.Background(), e.invite(t), "fixture_pending", testPassword)
	require.NoError(t, err)
	return u
}

// errRollback rolls back a transaction used only to look.
var errRollback = errors.New("rollback")

// requireUnderActive checks that every users.db value is under r's active key and decrypts.
func requireUnderActive(t *testing.T, e *authEnv, r *auth.Keyring) {
	t.Helper()
	err := e.users.InTx(context.Background(), func(ctx context.Context, tx *store.UsersTx) error {
		n, err := reencryptUsersDB(ctx, tx, r)
		require.NoError(t, err)
		require.Zero(t, n.total(), "every value is under key %d", r.ActiveID())
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	requireDecrypts(t, e, r)
}

// requireDecrypts opens every users.db value with r.
func requireDecrypts(t *testing.T, e *authEnv, r *auth.Keyring) {
	t.Helper()
	require.NoError(t, e.users.InReadTx(context.Background(), func(ctx context.Context, tx *store.UsersReadTx) error {
		users, err := tx.ListUsers(ctx)
		require.NoError(t, err)
		for _, u := range users {
			_, err := r.RewrapPasswordHash(auth.UserPassword(u.ID), auth.UserPassword(u.ID), u.PasswordPHC)
			require.NoError(t, err)
			_, err = r.DecryptTOTPSecret(auth.UserTOTP(u.ID), u.TOTPSecretEnc)
			require.NoError(t, err)
		}
		ens, err := tx.Enrollments(ctx)
		require.NoError(t, err)
		for _, en := range ens {
			_, err := r.RewrapPasswordHash(auth.EnrollmentPassword(en.InviteID), auth.EnrollmentPassword(en.InviteID), en.PasswordPHC)
			require.NoError(t, err)
			_, err = r.DecryptTOTPSecret(auth.EnrollmentTOTP(en.InviteID), en.TOTPSecretEnc)
			require.NoError(t, err)
		}
		codes, err := tx.RecoveryCodes(ctx)
		require.NoError(t, err)
		for _, c := range codes {
			_, err := r.MatchRecoveryCode(c.UserID, "x", [][]byte{c.CodeEnc}) // fails unless it opens
			require.NoError(t, err)
		}
		sks, err := tx.SigningKeys(ctx)
		require.NoError(t, err)
		for _, k := range sks {
			_, err := r.DecryptSigningSeed(k.ID, k.SeedEnc)
			require.NoError(t, err)
		}
		return nil
	}))
}

func TestRotateMasterKeyIfDue(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	seedSecrets(t, e)

	rotated, err := e.mk.RotateMasterKeyIfDue(ctx)
	require.NoError(t, err)
	assert.False(t, rotated, "a new key isn't due")
	e.advance(720*time.Hour - time.Second)
	rotated, err = e.mk.RotateMasterKeyIfDue(ctx)
	require.NoError(t, err)
	assert.False(t, rotated, "a second early")

	e.advance(time.Second)
	rotated, err = e.mk.RotateMasterKeyIfDue(ctx)
	require.NoError(t, err)
	assert.True(t, rotated)
	assert.Contains(t, e.logs.String(), fmt.Sprintf(`"event":"master_key_rotated","reason":"scheduled","old_key_id":1,"key_id":2,`+
		`"users":1,"enrollments":1,"recovery_codes":%d,"signing_keys":1`, auth.RecoveryCodeCount))
	mks := masterKeys(t, e.keys)
	require.Len(t, mks, 2)
	assert.True(t, mks[0].RetiredAt.Equal(e.clk.Now()), "key 1 retired now")
	assert.True(t, mks[1].RetiredAt.IsZero() && mks[1].CreatedAt.Equal(e.clk.Now()), "key 2 active")
	assert.EqualValues(t, 2, e.mk.Keyring().Load().ActiveID())
	requireUnderActive(t, e, e.mk.Keyring().Load())

	rotated, err = e.mk.RotateMasterKeyIfDue(ctx)
	require.NoError(t, err)
	assert.False(t, rotated, "the new key isn't due")
}

// TestMasterKeyRotation rotates and deletes the old key, checking that every kind of value
// keeps working: password and TOTP login, a recovery code, a pending signup, and access tokens.
func TestMasterKeyRotation(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := seedSecrets(t, e)
	before := e.login(t, u)

	rotatedAt := e.clk.Now()
	id, err := e.mk.RotateMasterKey(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 2, id)
	assert.Contains(t, e.logs.String(), `"event":"master_key_rotated","reason":"admin"`)
	requireUnderActive(t, e, e.mk.Keyring().Load())
	_, err = e.a.Authenticate(ctx, before.AccessToken)
	require.NoError(t, err, "an access token from before the rotation")
	e.login(t, u)

	// The retired key stays for masterKeyRetention, then goes at the first check after.
	e.advance(rotatedAt.Add(masterKeyRetention - time.Second).Sub(e.clk.Now()))
	n, err := e.mk.DeleteRetiredMasterKeys(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, []int64{1, 2}, masterKeyIDs(t, e.keys))
	e.advance(time.Second)
	n, err = e.mk.DeleteRetiredMasterKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, []int64{2}, masterKeyIDs(t, e.keys), "key 1 is gone from keys.db")
	assert.Contains(t, e.logs.String(), `"event":"master_key_deleted","key_id":1`)
	assert.NotContains(t, e.logs.String(), "master_key_straggler")

	// The keyring no longer holds key 1, and everything still works.
	r := e.mk.Keyring().Load()
	key1, err := auth.NewKeyring(1, []auth.MasterKey{{ID: 1, Key: auth.NewMasterKey()}})
	require.NoError(t, err)
	_, err = r.DecryptTOTPSecret(auth.UserTOTP(1), key1.EncryptTOTPSecret(auth.UserTOTP(1), []byte("x")))
	require.ErrorIs(t, err, auth.ErrKeyNotHeld)
	requireDecrypts(t, e, r)
	e.login(t, u)
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[0]})
	require.NoError(t, err, "a recovery code issued under key 1")
	other := e.signup(t, "fixture_other") // made under key 2
	e.login(t, other)

	// A restart over the same databases finds nothing to finish.
	_, err = NewMasterKeys(ctx, MasterKeysOptions{Keys: e.keys, Users: e.users, Logger: e.opts.Logger, Now: e.clk.Now})
	require.NoError(t, err)
	assert.NotContains(t, e.logs.String(), "master_key_reencrypted")
}

// TestMasterKeyRotationPendingSignup confirms a signup started under the old key after the
// rotation re-encrypted its enrollment.
func TestMasterKeyRotationPendingSignup(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	en, err := e.a.Signup(ctx, e.invite(t), "fixture_pending", testPassword)
	require.NoError(t, err)
	_, err = e.mk.RotateMasterKey(ctx)
	require.NoError(t, err)
	assert.Contains(t, e.logs.String(), `"users":0,"enrollments":1,"recovery_codes":0,"signing_keys":1`)
	secret := decodeSecret(t, en.TOTPSecret)
	c, err := e.a.ConfirmSignup(ctx, en.Token, auth.TOTPCode(secret, e.clk.Now()))
	require.NoError(t, err)
	e.login(t, testUser{id: c.UserID, name: "fixture_pending", secret: secret, codes: c.RecoveryCodes})
	requireUnderActive(t, e, e.mk.Keyring().Load())
}

// TestMasterKeySnapshotAcrossRotation: a keyring loaded before a rotation still decrypts what
// it read and encrypts values the live keyring decrypts.
func TestMasterKeySnapshotAcrossRotation(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	old := e.mk.Keyring().Load()
	enc := old.EncryptTOTPSecret(auth.UserTOTP(7), []byte("fixture secret"))

	_, err := e.mk.RotateMasterKey(ctx)
	require.NoError(t, err)
	live := e.mk.Keyring().Load()
	require.NotSame(t, old, live)

	got, err := old.DecryptTOTPSecret(auth.UserTOTP(7), enc)
	require.NoError(t, err)
	assert.Equal(t, []byte("fixture secret"), got)
	late := old.EncryptTOTPSecret(auth.UserTOTP(7), []byte("fixture late"))
	got, err = live.DecryptTOTPSecret(auth.UserTOTP(7), late)
	require.NoError(t, err)
	assert.Equal(t, []byte("fixture late"), got)
}

// TestMasterKeyStraggler: a value written under the retired key after the rotation's re-encrypt
// (a request holding an old snapshot) is re-encrypted before the key is deleted.
func TestMasterKeyStraggler(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := seedSecrets(t, e)
	old := e.mk.Keyring().Load()
	_, err := e.mk.RotateMasterKey(ctx)
	require.NoError(t, err)

	require.NoError(t, e.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		return tx.SetUserTOTP(ctx, u.id, old.EncryptTOTPSecret(auth.UserTOTP(u.id), u.secret), 0)
	}))
	e.advance(masterKeyRetention)
	n, err := e.mk.DeleteRetiredMasterKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Contains(t, e.logs.String(), `"level":"WARN","msg":"auth event","component":"auth","event":"master_key_straggler",`+
		`"key_ids":[1],"users":1,"enrollments":0,"recovery_codes":0,"signing_keys":0`)
	assert.Equal(t, []int64{2}, masterKeyIDs(t, e.keys))
	requireUnderActive(t, e, e.mk.Keyring().Load())
	e.login(t, u)
}

// TestMasterKeyCrashRecovery leaves the databases as a crash after each rotation step would,
// then restarts (NewMasterKeys) and runs the hourly checks, which finish the job.
func TestMasterKeyCrashRecovery(t *testing.T) {
	restart := func(t *testing.T, e *authEnv) *MasterKeys {
		t.Helper()
		m, err := NewMasterKeys(context.Background(), MasterKeysOptions{Keys: e.keys, Users: e.users,
			Logger: e.opts.Logger, Now: e.clk.Now})
		require.NoError(t, err)
		return m
	}
	finish := func(t *testing.T, e *authEnv, m *MasterKeys) {
		t.Helper()
		e.advance(masterKeyRetention)
		n, err := m.DeleteRetiredMasterKeys(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, []int64{2}, masterKeyIDs(t, e.keys))
		assert.NotContains(t, e.logs.String(), "master_key_straggler")
		requireUnderActive(t, e, m.Keyring().Load())
	}

	t.Run("after step 1 or 2", func(t *testing.T) {
		e := newAuthEnv(t)
		ctx := context.Background()
		seedSecrets(t, e)
		require.NoError(t, e.keys.InTx(ctx, func(ctx context.Context, tx *store.KeysTx) error {
			if err := tx.RetireMasterKey(ctx, 1, e.clk.Now()); err != nil {
				return err
			}
			_, err := tx.InsertMasterKey(ctx, auth.NewMasterKey(), e.clk.Now())
			return err
		}))
		m := restart(t, e)
		assert.Contains(t, e.logs.String(), fmt.Sprintf(`"event":"master_key_reencrypted","reason":"startup","key_id":2,`+
			`"users":1,"enrollments":1,"recovery_codes":%d,"signing_keys":1`, auth.RecoveryCodeCount))
		requireUnderActive(t, e, m.Keyring().Load())
		finish(t, e, m)
	})
	t.Run("after step 3", func(t *testing.T) {
		e := newAuthEnv(t)
		seedSecrets(t, e)
		_, err := e.mk.RotateMasterKey(context.Background())
		require.NoError(t, err)
		m := restart(t, e)
		assert.NotContains(t, e.logs.String(), "master_key_reencrypted")
		assert.Equal(t, []int64{1, 2}, masterKeyIDs(t, e.keys), "kept until its retention passes")
		finish(t, e, m)
	})
	t.Run("after step 4", func(t *testing.T) {
		e := newAuthEnv(t)
		ctx := context.Background()
		seedSecrets(t, e)
		_, err := e.mk.RotateMasterKey(ctx)
		require.NoError(t, err)
		e.advance(masterKeyRetention)
		_, err = e.mk.reencrypt(ctx, e.mk.Keyring().Load()) // step 4, then the crash
		require.NoError(t, err)
		m := restart(t, e)
		n, err := m.DeleteRetiredMasterKeys(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, []int64{2}, masterKeyIDs(t, e.keys))
		requireUnderActive(t, e, m.Keyring().Load())
	})
}

// TestMasterKeyConcurrentTriggers: two due checks (or a check and an admin) at once rotate once
// per trigger that finds the key due, never twice for one due key.
func TestMasterKeyConcurrentTriggers(t *testing.T) {
	e := newAuthEnv(t)
	seedSecrets(t, e)
	e.advance(720 * time.Hour)
	var wg sync.WaitGroup
	results := make([]bool, 4)
	for i := range results {
		wg.Go(func() {
			rotated, err := e.mk.RotateMasterKeyIfDue(context.Background())
			assert.NoError(t, err)
			results[i] = rotated
		})
	}
	wg.Wait()
	assert.Equal(t, 1, strings.Count(fmt.Sprint(results), "true"), "rotated once: %v", results)
	assert.Equal(t, []int64{1, 2}, masterKeyIDs(t, e.keys))
	requireUnderActive(t, e, e.mk.Keyring().Load())
}

// TestMasterKeyMissingKeyRefusesStart: a users.db with values under a key keys.db doesn't hold
// (restored with another keys.db) stops serve at startup.
func TestMasterKeyMissingKeyRefusesStart(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	seedSecrets(t, e)
	_, err := e.mk.RotateMasterKey(ctx) // everything is under key 2 now
	require.NoError(t, err)

	other, err := store.OpenKeys(ctx, filepath.Join(t.TempDir(), "api"), store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	_, err = NewMasterKeys(ctx, MasterKeysOptions{Keys: other, Users: e.users, Logger: e.opts.Logger, Now: e.clk.Now})
	require.ErrorIs(t, err, auth.ErrKeyNotHeld)
	require.ErrorContains(t, err, "users.db holds a value under a master key keys.db does not have")
}
