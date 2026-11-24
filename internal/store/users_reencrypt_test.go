package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// TestReencryptQueries covers the reads and writes master key rotation uses: each setter
// replaces only the encrypted values, and a missing row is its not-found error.
func TestReencryptQueries(t *testing.T) {
	_, tx := openUsersTest(t)
	ctx := context.Background()
	uid, err := tx.CreateUser(ctx, fixtureUser("fixture_a"))
	require.NoError(t, err)
	require.NoError(t, tx.SetUserTOTPStep(ctx, uid, 42))
	require.NoError(t, tx.SetUserSecrets(ctx, uid, "$argon2id$fixture2", []byte{7}))
	u, err := tx.UserByID(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, "$argon2id$fixture2", u.PasswordPHC)
	assert.Equal(t, []byte{7}, u.TOTPSecretEnc)
	assert.EqualValues(t, 42, u.LastTOTPStep, "kept")
	assert.True(t, u.PasswordChangedAt.Equal(usersT0), "kept")
	require.ErrorIs(t, tx.SetUserSecrets(ctx, uid+1, fixturePHC, []byte{1}), ErrNoUser)

	// Enrollments: a signup (both values) and a reset keeping both ("" and NULL).
	var invites [2]int64
	for i := range invites {
		invites[i], err = tx.CreateInvite(ctx, model.Invite{Kind: model.InviteSignup, CreatedAt: usersT0,
			ExpiresAt: usersT0.Add(time.Hour)}, sum("inv"+string(rune('a'+i))))
		require.NoError(t, err)
	}
	require.NoError(t, tx.PutEnrollment(ctx, model.Enrollment{InviteID: invites[0], Username: "fixture_b",
		PasswordPHC: fixturePHC, TOTPSecretEnc: []byte{9}, ExpiresAt: usersT0}, sum("enr1")))
	require.NoError(t, tx.PutEnrollment(ctx, model.Enrollment{InviteID: invites[1], ExpiresAt: usersT0}, sum("enr2")))
	require.NoError(t, tx.SetEnrollmentSecrets(ctx, invites[0], "$argon2id$fixture3", []byte{8}))
	ens, err := tx.Enrollments(ctx)
	require.NoError(t, err)
	require.Len(t, ens, 2)
	assert.Equal(t, "$argon2id$fixture3", ens[0].PasswordPHC)
	assert.Equal(t, []byte{8}, ens[0].TOTPSecretEnc)
	assert.Equal(t, "fixture_b", ens[0].Username)
	assert.Empty(t, ens[1].PasswordPHC)
	assert.Nil(t, ens[1].TOTPSecretEnc, "NULL keeps meaning keep the current TOTP")
	require.ErrorIs(t, tx.SetEnrollmentSecrets(ctx, invites[1]+1, "", nil), ErrNoEnrollment)

	// Recovery codes, used ones included, with their user.
	require.NoError(t, tx.ReplaceRecoveryCodes(ctx, uid, [][]byte{{1}, {2}}))
	codes, err := tx.UnusedRecoveryCodes(ctx, uid)
	require.NoError(t, err)
	ok, err := tx.UseRecoveryCode(ctx, uid, codes[0].ID, usersT0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, tx.SetRecoveryCodeEnc(ctx, codes[0].ID, []byte{3}))
	all, err := tx.RecoveryCodes(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, model.RecoveryCode{ID: codes[0].ID, UserID: uid, CodeEnc: []byte{3}}, all[0])
	assert.Equal(t, model.RecoveryCode{ID: codes[1].ID, UserID: uid, CodeEnc: []byte{2}}, all[1])
	require.ErrorIs(t, tx.SetRecoveryCodeEnc(ctx, codes[1].ID+1, []byte{1}), ErrNoRecoveryCode)

	// Signing keys.
	require.NoError(t, tx.AddSigningKey(ctx, model.SigningKey{ID: "k1", SeedEnc: []byte{1}, CreatedAt: usersT0}))
	require.NoError(t, tx.SetSigningKeySeed(ctx, "k1", []byte{5}))
	keys, err := tx.SigningKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, []byte{5}, keys[0].SeedEnc)
	assert.True(t, keys[0].CreatedAt.Equal(usersT0))
	require.ErrorIs(t, tx.SetSigningKeySeed(ctx, "k2", []byte{1}), ErrNoSigningKey)
}
