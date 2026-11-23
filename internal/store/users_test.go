package store

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

var usersT0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// fixturePHC stands in for a stored hash; the store never parses it.
const fixturePHC = "$argon2id$fixture"

func openUsersTest(t *testing.T) (*Users, *UsersTx) {
	t.Helper()
	u, err := OpenUsers(context.Background(), filepath.Join(t.TempDir(), "api"), Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, u.Close()) })
	return u, u.AutoCommitForTestOnly()
}

func sum(s string) [32]byte { return sha256.Sum256([]byte(s)) }

func fixtureUser(name string) model.User {
	return model.User{Username: name, PasswordPHC: fixturePHC, TOTPSecretEnc: []byte{1, 2, 3},
		CreatedAt: usersT0, PasswordChangedAt: usersT0}
}

func TestOpenUsersFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "api")
	u, err := OpenUsers(context.Background(), dir, Options{})
	require.NoError(t, err)
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	// Checked while open: SQLite removes -wal and -shm on a clean close.
	for _, f := range []string{UsersFile, UsersFile + "-wal", UsersFile + "-shm"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		require.NoError(t, err, f)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), f)
	}
	require.NoError(t, u.Close())

	// Reopening is fine; a database others can read is refused.
	u, err = OpenUsers(context.Background(), dir, Options{})
	require.NoError(t, err)
	require.NoError(t, u.Close())
	require.NoError(t, os.Chmod(filepath.Join(dir, UsersFile), 0o644)) //nolint:gosec // G302: the test needs a world-readable db
	_, err = OpenUsers(context.Background(), dir, Options{})
	require.ErrorContains(t, err, "chmod 600")
}

func TestUsersTxGuard(t *testing.T) {
	u, _ := openUsersTest(t)
	s, _, _ := openTest(t)
	ctx := context.Background()
	err := u.InTx(ctx, func(ctx context.Context, _ *UsersTx) error {
		return u.InReadTx(ctx, func(context.Context, *UsersReadTx) error { return nil })
	})
	require.ErrorIs(t, err, ErrTxMisuse)
	// Neither database can be opened inside the other's transaction.
	err = s.st.InReadTx(ctx, func(ctx context.Context, _ *ReadTx) error {
		return u.InReadTx(ctx, func(context.Context, *UsersReadTx) error { return nil })
	})
	require.ErrorIs(t, err, ErrTxMisuse)
	err = u.InReadTx(ctx, func(ctx context.Context, _ *UsersReadTx) error {
		return s.st.InReadTx(ctx, func(context.Context, *ReadTx) error { return nil })
	})
	require.ErrorIs(t, err, ErrTxMisuse)
}

func TestUsersInTxRollsBack(t *testing.T) {
	u, tx := openUsersTest(t)
	ctx := context.Background()
	err := u.InTx(ctx, func(ctx context.Context, tx *UsersTx) error {
		if _, err := tx.CreateUser(ctx, fixtureUser("fixture_a")); err != nil {
			return err
		}
		return os.ErrClosed
	})
	require.ErrorIs(t, err, os.ErrClosed)
	taken, err := tx.UsernameTaken(ctx, "fixture_a")
	require.NoError(t, err)
	assert.False(t, taken)
}

func TestUserLifecycle(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	id, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)

	got, err := tx.UserByName(ctx, "fixture_a")
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, usersT0, got.CreatedAt.UTC())
	assert.False(t, got.Disabled())
	_, err = tx.UserByName(ctx, "fixture_b")
	require.ErrorIs(t, err, ErrNoUser)

	require.NoError(t, tx.SetUserTOTPStep(ctx, id, 10))
	require.NoError(t, tx.SetUserTOTPStep(ctx, id, 5)) // never backwards
	require.NoError(t, tx.SetUserAttempts(ctx, id, 3, usersT0.Add(time.Minute)))
	got, err = tx.UserByID(ctx, id)
	require.NoError(t, err)
	assert.EqualValues(t, 10, got.LastTOTPStep)
	assert.Equal(t, 3, got.FailedAttempts)
	assert.Equal(t, usersT0.Add(time.Minute), got.NextAttemptAt.UTC())

	require.NoError(t, tx.RecordUserLogin(ctx, id, usersT0.Add(time.Hour)))
	require.NoError(t, tx.DisableUser(ctx, id, usersT0))
	require.NoError(t, tx.DisableUser(ctx, id, usersT0.Add(time.Hour))) // keeps the first time
	got, err = tx.UserByID(ctx, id)
	require.NoError(t, err)
	assert.Zero(t, got.FailedAttempts)
	assert.True(t, got.NextAttemptAt.IsZero())
	assert.Equal(t, usersT0, got.DisabledAt.UTC())
	require.NoError(t, tx.EnableUser(ctx, id))

	ok, err := tx.RehashUserPassword(ctx, id, "$argon2id$other", "$argon2id$new")
	require.NoError(t, err)
	assert.False(t, ok, "a changed hash is not overwritten")
	ok, err = tx.RehashUserPassword(ctx, id, fixturePHC, "$argon2id$new")
	require.NoError(t, err)
	assert.True(t, ok)

	require.NoError(t, tx.DeleteUser(ctx, id))
	require.ErrorIs(t, tx.DeleteUser(ctx, id), ErrNoUser)
	require.ErrorIs(t, tx.SetUserAttempts(ctx, id, 1, usersT0), ErrNoUser)
}

func TestUserIDsAreNotReused(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	a, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	require.NoError(t, tx.DeleteUser(ctx, a))
	b, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	assert.Greater(t, b, a)
}

func TestRecoveryCodesSingleUse(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	id, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	other, err := tx.CreateUser(ctx, fixtureUser("fixture_b"))
	require.NoError(t, err)
	require.NoError(t, tx.ReplaceRecoveryCodes(ctx, id, [][]byte{{'a'}, {'b'}}))
	require.NoError(t, tx.ReplaceRecoveryCodes(ctx, other, [][]byte{{'o'}}))
	codes, err := tx.UnusedRecoveryCodes(ctx, id)
	require.NoError(t, err)
	require.Len(t, codes, 2)
	assert.Equal(t, []byte{'a'}, codes[0].CodeEnc)
	assert.Equal(t, []byte{'b'}, codes[1].CodeEnc)

	ok, err := tx.UseRecoveryCode(ctx, id, codes[0].ID, usersT0)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = tx.UseRecoveryCode(ctx, id, codes[0].ID, usersT0)
	require.NoError(t, err)
	assert.False(t, ok, "used twice")
	ok, err = tx.UseRecoveryCode(ctx, other, codes[1].ID, usersT0)
	require.NoError(t, err)
	assert.False(t, ok, "another user's code")
	n, err := tx.RecoveryCodesLeft(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	left, err := tx.UnusedRecoveryCodes(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, codes[1:], left, "only unused codes")

	require.NoError(t, tx.ReplaceRecoveryCodes(ctx, id, [][]byte{{'c'}}))
	ok, err = tx.UseRecoveryCode(ctx, id, codes[1].ID, usersT0)
	require.NoError(t, err)
	assert.False(t, ok, "replaced")
	n, err = tx.RecoveryCodesLeft(ctx, other)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the other user's codes are untouched")
}

func TestInvitesAndEnrollments(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	inv := model.Invite{Kind: model.InviteSignup, CreatedAt: usersT0, ExpiresAt: usersT0.Add(24 * time.Hour)}
	id, err := tx.CreateInvite(ctx, inv, sum("inv1"))
	require.NoError(t, err)
	got, err := tx.InviteByToken(ctx, sum("inv1"))
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.True(t, inv.ExpiresAt.Equal(got.ExpiresAt), "expires_at round-trips")
	assert.True(t, got.UsedAt.IsZero() && got.RevokedAt.IsZero())
	_, err = tx.InviteByToken(ctx, sum("nope"))
	require.ErrorIs(t, err, ErrNoInvite)

	// A signup invite has no target and no scope; a reset invite has both.
	_, err = tx.CreateInvite(ctx, model.Invite{Kind: model.InviteSignup, Scope: model.ResetBoth,
		CreatedAt: usersT0, ExpiresAt: usersT0}, sum("bad"))
	require.Error(t, err)

	// Step 1 again replaces the enrollment.
	e := model.Enrollment{InviteID: id, Username: "fixture_a", PasswordPHC: fixturePHC, TOTPSecretEnc: []byte{9},
		ExpiresAt: usersT0.Add(15 * time.Minute)}
	require.NoError(t, tx.PutEnrollment(ctx, e, sum("enr1")))
	require.NoError(t, tx.PutEnrollment(ctx, e, sum("enr2")))
	_, err = tx.EnrollmentByToken(ctx, sum("enr1"))
	require.ErrorIs(t, err, ErrNoEnrollment)
	gotE, err := tx.EnrollmentByToken(ctx, sum("enr2"))
	require.NoError(t, err)
	assert.Equal(t, e.Username, gotE.Username)
	assert.Equal(t, []byte{9}, gotE.TOTPSecretEnc)

	uid, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	ok, err := tx.UseInvite(ctx, id, uid, usersT0)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = tx.UseInvite(ctx, id, uid, usersT0)
	require.NoError(t, err)
	assert.False(t, ok, "used twice")

	// Revoking deletes the pending enrollment; a revoked invite can't be used.
	id2, err := tx.CreateInvite(ctx, model.Invite{Kind: model.InviteReset, TargetUserID: uid, Scope: model.ResetTOTP,
		CreatedAt: usersT0, ExpiresAt: usersT0.Add(time.Hour)}, sum("inv2"))
	require.NoError(t, err)
	require.NoError(t, tx.PutEnrollment(ctx, model.Enrollment{InviteID: id2, ExpiresAt: usersT0.Add(time.Hour)}, sum("enr3")))
	require.NoError(t, tx.RevokeInvite(ctx, id2, usersT0))
	_, err = tx.EnrollmentByToken(ctx, sum("enr3"))
	require.ErrorIs(t, err, ErrNoEnrollment)
	ok, err = tx.UseInvite(ctx, id2, uid, usersT0)
	require.NoError(t, err)
	assert.False(t, ok)
	require.ErrorIs(t, tx.RevokeInvite(ctx, 999, usersT0), ErrNoInvite)

	list, err := tx.ListInvites(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, id2, list[0].ID)
	assert.Equal(t, uid, list[0].TargetUserID)
	assert.Equal(t, model.ResetTOTP, list[0].Scope)
	assert.Equal(t, uid, list[1].UsedBy)

	// Deleting the user deletes its reset invites and clears used_by.
	require.NoError(t, tx.DeleteUser(ctx, uid))
	_, err = tx.InviteByID(ctx, id2)
	require.ErrorIs(t, err, ErrNoInvite)
	got, err = tx.InviteByID(ctx, id)
	require.NoError(t, err)
	assert.Zero(t, got.UsedBy)
	assert.False(t, got.UsedAt.IsZero())
}

func TestChallenges(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	uid, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	require.NoError(t, tx.CreateChallenge(ctx, sum("c1"), model.LoginChallenge{UserID: uid, ExpiresAt: usersT0.Add(5 * time.Minute)}))
	require.NoError(t, tx.CreateChallenge(ctx, sum("c2"), model.LoginChallenge{UserID: uid, ExpiresAt: usersT0}))
	require.NoError(t, tx.SetChallengeAttempts(ctx, sum("c1"), 2))
	c, err := tx.ChallengeByToken(ctx, sum("c1"))
	require.NoError(t, err)
	assert.Equal(t, 2, c.Attempts)
	require.NoError(t, tx.DeleteExpiredChallenges(ctx, usersT0))
	_, err = tx.ChallengeByToken(ctx, sum("c2"))
	require.ErrorIs(t, err, ErrNoChallenge)
	require.NoError(t, tx.DeleteChallenge(ctx, sum("c1")))
	require.ErrorIs(t, tx.SetChallengeAttempts(ctx, sum("c1"), 3), ErrNoChallenge)
}

func TestSessions(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	a, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	b, err := tx.CreateUser(ctx, fixtureUser("fixture_b"))
	require.NoError(t, err)
	exp := usersT0.Add(30 * 24 * time.Hour)
	s1, err := tx.CreateSession(ctx, a, sum("r1"), usersT0, exp)
	require.NoError(t, err)
	s2, err := tx.CreateSession(ctx, a, sum("r2"), usersT0, exp)
	require.NoError(t, err)
	sb, err := tx.CreateSession(ctx, b, sum("rb"), usersT0, exp)
	require.NoError(t, err)

	// Rotation: the old token becomes the previous one; rotating from it again does nothing.
	later := usersT0.Add(time.Hour)
	ok, err := tx.RotateSession(ctx, s1, sum("r1"), sum("r1b"), later, later.Add(30*24*time.Hour))
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = tx.RotateSession(ctx, s1, sum("r1"), sum("r1c"), later, later)
	require.NoError(t, err)
	assert.False(t, ok)
	got, err := tx.SessionByRefresh(ctx, sum("r1b"))
	require.NoError(t, err)
	assert.Equal(t, s1, got.ID)
	assert.Equal(t, later, got.LastUsedAt.UTC())
	got, err = tx.SessionByPrevRefresh(ctx, sum("r1"))
	require.NoError(t, err)
	assert.Equal(t, s1, got.ID)
	_, err = tx.SessionByRefresh(ctx, sum("r1"))
	require.ErrorIs(t, err, ErrNoSession)

	// User A can't revoke user B's session: the same error as a missing one.
	require.ErrorIs(t, tx.RevokeUserSession(ctx, a, sb, usersT0), ErrNoSession)
	require.ErrorIs(t, tx.RevokeUserSession(ctx, a, 999, usersT0), ErrNoSession)
	got, err = tx.SessionByID(ctx, sb)
	require.NoError(t, err)
	assert.True(t, got.Active(usersT0))
	require.NoError(t, tx.RevokeUserSession(ctx, a, s2, usersT0))

	list, err := tx.UserSessions(ctx, a, usersT0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, s1, list[0].ID)

	// A revoked session can't rotate.
	ok, err = tx.RotateSession(ctx, s2, sum("r2"), sum("r2b"), later, later)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, tx.RevokeUserSessions(ctx, a, s1, usersT0))
	got, err = tx.SessionByID(ctx, s1)
	require.NoError(t, err)
	assert.True(t, got.Active(usersT0), "kept")
	require.NoError(t, tx.RevokeUserSessions(ctx, a, 0, usersT0))
	got, err = tx.SessionByID(ctx, s1)
	require.NoError(t, err)
	assert.False(t, got.Active(usersT0))

	require.NoError(t, tx.DeleteExpiredSessions(ctx, exp))
	_, err = tx.SessionByID(ctx, s2)
	require.ErrorIs(t, err, ErrNoSession)
	_, err = tx.SessionByID(ctx, s1)
	require.NoError(t, err, "extended by rotation")

	require.NoError(t, tx.DeleteUser(ctx, b))
	_, err = tx.SessionByID(ctx, sb)
	require.ErrorIs(t, err, ErrNoSession)
}

func TestSigningKeys(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	require.NoError(t, tx.AddSigningKey(ctx, model.SigningKey{ID: "k1", SeedEnc: []byte{1}, CreatedAt: usersT0}))
	require.NoError(t, tx.RetireSigningKeys(ctx, usersT0.Add(time.Hour)))
	require.NoError(t, tx.AddSigningKey(ctx, model.SigningKey{ID: "k2", SeedEnc: []byte{2}, CreatedAt: usersT0.Add(time.Hour)}))
	keys, err := tx.SigningKeys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, "k1", keys[0].ID)
	assert.Equal(t, []byte{1}, keys[0].SeedEnc)
	assert.Equal(t, usersT0.Add(time.Hour), keys[0].RetiredAt.UTC())
	assert.True(t, keys[1].RetiredAt.IsZero())

	require.NoError(t, tx.DeleteSigningKeysRetiredBy(ctx, usersT0.Add(time.Hour-time.Second)))
	keys, err = tx.SigningKeys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 2, "retired after t")
	require.NoError(t, tx.DeleteSigningKeysRetiredBy(ctx, usersT0.Add(time.Hour)))
	keys, err = tx.SigningKeys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "k2", keys[0].ID)
}
