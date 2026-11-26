package integration

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/dirs/dirstest"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Fixed ids of the seeded accounts (a fresh users.db numbers rows from 1). Usernames are not
// fanwork: the API returns them verbatim (dto.Username).
const (
	// seedUserName, with seedSession (seedRefreshToken) and a pending login (seedChallengeToken);
	// it logs in with fixturePassword, seedTOTPSecret and seedRecoveryCode.
	seedUser      int64 = 1
	seedOtherUser int64 = 2 // seedOtherName, disabled, no sessions
	seedSession   int64 = 1
	seedInvite    int64 = 1 // a usable signup invite (seedInviteToken), with a pending signup
	seedReset     int64 = 2 // a usable TOTP reset of seedUser (seedResetToken), with a pending reset

	seedUserName  = "fixture_user"
	seedOtherName = "fixture_other"
	seedNewName   = "fixture_new" // the pending signup's username
)

// fixtureToken is a well-formed opaque token of kind: tag, padded with base64url zero bits.
func fixtureToken(kind auth.TokenKind, tag string) string {
	return string(kind) + tag + strings.Repeat("A", 43-len(tag))
}

// The seeded tokens (stored as their hashes, like real ones) and the TOTP secret of the
// accounts and the pending enrollments, whose code at seedTime confirms them or logs in.
var (
	seedInviteToken       = fixtureToken(auth.InviteToken, "fixtureSignupInvite")
	seedResetToken        = fixtureToken(auth.InviteToken, "fixtureResetInvite")
	seedSignupEnrollToken = fixtureToken(auth.EnrollmentToken, "fixtureSignup")
	seedResetEnrollToken  = fixtureToken(auth.EnrollmentToken, "fixtureReset")
	seedChallengeToken    = fixtureToken(auth.ChallengeToken, "fixtureLogin")
	seedRefreshToken      = fixtureToken(auth.RefreshToken, "fixtureRefresh")
	seedTOTPSecret        = []byte("fixture-totp-secret!") // 20 bytes
	seedTOTPCode          = auth.TOTPCode(seedTOTPSecret, seedTime)
)

// seedRecoveryCode is seedUser's only recovery code.
const seedRecoveryCode = "fixt-urec-ovry-code-seed-user"

// tokenSum is a seeded token's stored hash.
func tokenSum(tb testing.TB, kind auth.TokenKind, token string) [32]byte {
	tb.Helper()
	sum, ok := auth.TokenSum(kind, token)
	require.True(tb, ok, "fixture token %s", kind)
	return sum
}

// testPublicURL is the seeded Auth controller's api.public_url (the invite links' origin).
const testPublicURL = "https://mirror.example.com"

// cheapArgon2 keeps the tests fast.
var cheapArgon2 = auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

// lastBackupDir is the backup directory (api.backup_dir) of the Auth controller newTestAuth
// made last; the tests don't run in parallel.
var lastBackupDir string

// newTestAuth opens a temp users.db and keys.db, seeds the accounts above and returns the
// Auth controller over them (clocked at seedTime, logging to log).
func newTestAuth(tb testing.TB, log *slog.Logger) *controllers.Auth {
	tb.Helper()
	ctx := context.Background()
	base := tb.TempDir()
	dir := filepath.Join(base, "api")
	lastBackupDir = filepath.Join(base, "backups")
	api := dirstest.At(tb, dir, 0o700)
	users, err := store.OpenUsers(ctx, api, store.Options{Logger: log})
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, users.Close()) })
	keys, err := store.OpenKeys(ctx, api, store.Options{Logger: log})
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, keys.Close()) })
	mk, err := controllers.NewMasterKeys(ctx, controllers.MasterKeysOptions{Keys: keys, Users: users, Logger: log,
		Now: func() time.Time { return seedTime }, BackupDir: dirstest.At(tb, lastBackupDir, 0o700)})
	require.NoError(tb, err)
	secrets := mk.Keyring()
	hasher, err := auth.NewHasher(secrets, auth.HasherOptions{Params: cheapArgon2})
	require.NoError(tb, err)
	// Outside the transaction (txguard). Each hash is wrapped for its row: users 1 and 2 and the
	// signup enrollment.
	var phcs [2]string
	for i := range phcs {
		var err error
		phcs[i], err = hasher.Hash(ctx, auth.UserPassword(int64(i+1)), fixturePassword)
		require.NoError(tb, err)
	}
	enrollPHC, err := hasher.Hash(ctx, auth.EnrollmentPassword(seedInvite), fixturePassword)
	require.NoError(tb, err)
	recovery, ok := auth.NormalizeRecoveryCode(seedRecoveryCode)
	require.True(tb, ok, "seedRecoveryCode is well-formed")
	require.NoError(tb, users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		for i, name := range []string{seedUserName, seedOtherName} {
			id := int64(i + 1)
			if _, err := tx.CreateUser(ctx, model.User{Username: name, PasswordPHC: phcs[i],
				TOTPSecretEnc: secrets.Load().EncryptTOTPSecret(auth.UserTOTP(id), seedTOTPSecret),
				CreatedAt:     seedTime.Add(-time.Hour), PasswordChangedAt: seedTime.Add(-time.Hour)}); err != nil {
				return err
			}
		}
		if err := tx.DisableUser(ctx, seedOtherUser, seedTime.Add(-time.Minute)); err != nil {
			return err
		}
		if err := tx.ReplaceRecoveryCodes(ctx, seedUser, [][]byte{secrets.Load().EncryptRecoveryCode(seedUser, recovery)}); err != nil {
			return err
		}
		if _, err := tx.CreateSession(ctx, seedUser, tokenSum(tb, auth.RefreshToken, seedRefreshToken),
			seedTime.Add(-time.Minute), seedTime.Add(24*time.Hour)); err != nil {
			return err
		}
		if err := tx.CreateChallenge(ctx, tokenSum(tb, auth.ChallengeToken, seedChallengeToken),
			model.LoginChallenge{UserID: seedUser, ExpiresAt: seedTime.Add(controllers.ChallengeTTL)}); err != nil {
			return err
		}
		if _, err := tx.CreateInvite(ctx, model.Invite{Kind: model.InviteSignup, CreatedAt: seedTime.Add(-time.Minute),
			ExpiresAt: seedTime.Add(time.Hour)}, tokenSum(tb, auth.InviteToken, seedInviteToken)); err != nil {
			return err
		}
		if _, err := tx.CreateInvite(ctx, model.Invite{Kind: model.InviteReset, TargetUserID: seedUser, Scope: model.ResetTOTP,
			CreatedAt: seedTime.Add(-time.Minute), ExpiresAt: seedTime.Add(time.Hour)},
			tokenSum(tb, auth.InviteToken, seedResetToken)); err != nil {
			return err
		}
		if err := tx.PutEnrollment(ctx, model.Enrollment{InviteID: seedInvite, Username: seedNewName, PasswordPHC: enrollPHC,
			TOTPSecretEnc: secrets.Load().EncryptTOTPSecret(auth.EnrollmentTOTP(seedInvite), seedTOTPSecret),
			ExpiresAt:     seedTime.Add(10 * time.Minute)}, tokenSum(tb, auth.EnrollmentToken, seedSignupEnrollToken)); err != nil {
			return err
		}
		return tx.PutEnrollment(ctx, model.Enrollment{InviteID: seedReset,
			TOTPSecretEnc: secrets.Load().EncryptTOTPSecret(auth.EnrollmentTOTP(seedReset), seedTOTPSecret),
			ExpiresAt:     seedTime.Add(10 * time.Minute)}, tokenSum(tb, auth.EnrollmentToken, seedResetEnrollToken))
	}))
	a, err := controllers.NewAuth(ctx, controllers.AuthOptions{Users: users, MasterKeys: mk, Hasher: hasher, Logger: log,
		Now: func() time.Time { return seedTime }, PublicURL: testPublicURL})
	require.NoError(tb, err)
	return a
}
