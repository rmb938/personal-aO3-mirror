package integration

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Fixed ids of the seeded accounts (a fresh users.db numbers rows from 1). Usernames are not
// fanwork: the API returns them verbatim (dto.Username).
const (
	seedUser      int64 = 1 // seedUserName, with seedSession
	seedOtherUser int64 = 2 // seedOtherName, disabled, no sessions
	seedSession   int64 = 1
	seedInvite    int64 = 1 // a usable signup invite

	seedUserName  = "fixture_user"
	seedOtherName = "fixture_other"
)

// testPublicURL is the seeded Auth controller's api.public_url (the invite links' origin).
const testPublicURL = "https://mirror.example.com"

// cheapArgon2 keeps the tests fast (nothing here hashes a password, but NewHasher needs
// parameters).
var cheapArgon2 = auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

// newTestAuth opens a temp users.db and auth.key, seeds the accounts above and returns the
// Auth controller over them (clocked at seedTime, logging to log).
func newTestAuth(tb testing.TB, log *slog.Logger) *controllers.Auth {
	tb.Helper()
	ctx := context.Background()
	dir := filepath.Join(tb.TempDir(), "api")
	users, err := store.OpenUsers(ctx, dir, store.Options{Logger: log})
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, users.Close()) })
	secrets, err := auth.LoadOrCreateSecrets(filepath.Join(dir, auth.KeyFileName))
	require.NoError(tb, err)
	hasher, err := auth.NewHasher(auth.HasherOptions{Params: cheapArgon2})
	require.NoError(tb, err)
	require.NoError(tb, users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		for _, name := range []string{seedUserName, seedOtherName} {
			// Neither stored secret is ever checked here; they only fill the NOT NULL columns.
			if _, err := tx.CreateUser(ctx, model.User{Username: name, PasswordPHC: "$fixture", TOTPSecretEnc: []byte("fixture"),
				CreatedAt: seedTime.Add(-time.Hour), PasswordChangedAt: seedTime.Add(-time.Hour)}); err != nil {
				return err
			}
		}
		if err := tx.DisableUser(ctx, seedOtherUser, seedTime.Add(-time.Minute)); err != nil {
			return err
		}
		if _, err := tx.CreateSession(ctx, seedUser, sha256.Sum256([]byte("fixture refresh")), seedTime.Add(-time.Minute),
			seedTime.Add(24*time.Hour)); err != nil {
			return err
		}
		_, err := tx.CreateInvite(ctx, model.Invite{Kind: model.InviteSignup, CreatedAt: seedTime.Add(-time.Minute),
			ExpiresAt: seedTime.Add(time.Hour)}, sha256.Sum256([]byte("fixture invite")))
		return err
	}))
	a, err := controllers.NewAuth(ctx, controllers.AuthOptions{Users: users, Secrets: secrets, Hasher: hasher, Logger: log,
		Now: func() time.Time { return seedTime }, PublicURL: testPublicURL})
	require.NoError(tb, err)
	return a
}
