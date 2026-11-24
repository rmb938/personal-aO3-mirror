package main

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// seedAccounts writes two accounts (ids 1 and 2) and a session for account 1 (id 1) into the
// CLI's temp users.db. Run it before c.serve, which opens the same database.
func (c *cli) seedAccounts() {
	c.t.Helper()
	ctx := context.Background()
	cfg := c.config()
	dir := filepath.Dir(cfg.Path(cfg.API.AdminSocket))
	require.NoError(c.t, os.MkdirAll(dir, 0o700))
	users, err := store.OpenUsers(ctx, dir, store.Options{})
	require.NoError(c.t, err)
	defer func() { require.NoError(c.t, users.Close()) }()
	keys, err := store.OpenKeys(ctx, dir, store.Options{})
	require.NoError(c.t, err)
	defer func() { require.NoError(c.t, keys.Close()) }()
	// Nothing here logs in, but serve checks at startup that every secret decrypts, so they are
	// real values under keys.db's key.
	mk, err := controllers.NewMasterKeys(ctx, controllers.MasterKeysOptions{Keys: keys, Users: users})
	require.NoError(c.t, err)
	hasher, err := auth.NewHasher(mk.Keyring(), auth.HasherOptions{
		Params: auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	require.NoError(c.t, err)
	var phcs [2]string
	for i := range phcs {
		phcs[i], err = hasher.Hash(ctx, auth.UserPassword(int64(i+1)), "fixture horse staple")
		require.NoError(c.t, err)
	}
	now := time.Now()
	require.NoError(c.t, users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		for i, name := range []string{"fixture_user", "fixture_other"} {
			id := int64(i + 1)
			if _, err := tx.CreateUser(ctx, model.User{Username: name, PasswordPHC: phcs[i],
				TOTPSecretEnc: mk.Keyring().Load().EncryptTOTPSecret(auth.UserTOTP(id), []byte("fixture")),
				CreatedAt:     now.Add(-time.Hour), PasswordChangedAt: now.Add(-time.Hour)}); err != nil {
				return err
			}
		}
		_, err := tx.CreateSession(ctx, 1, sha256.Sum256([]byte("fixture refresh")), now.Add(-time.Minute), now.Add(24*time.Hour))
		return err
	}))
}

func TestAccountCLI(t *testing.T) {
	c := newCLI(t, "")
	c.seedAccounts()
	c.serve(false)

	code, out, errOut := c.run("invite", "create", "--ttl", "2h")
	require.Equal(t, 0, code, "invite create: %s", errOut)
	assert.Contains(t, out, "signup invite created as invite 1", "invite create")
	assert.Contains(t, out, "http://localhost:8080/signup#token=", "invite create prints the link")
	code, _, errOut = c.run("invite", "create", "--ttl", "1s")
	assert.Equal(t, 1, code, "invite create with a too-short ttl")
	assert.Contains(t, errOut, "/ttl:", "a validation error names the invalid member")

	code, out, errOut = c.run("user", "list")
	require.Equal(t, 0, code, "user list: %s", errOut)
	assert.Contains(t, out, "fixture_user", "usernames are verbatim")
	assert.Contains(t, out, "fixture_other", "usernames are verbatim")

	code, out, errOut = c.run("user", "sessions", "1")
	require.Equal(t, 0, code, "user sessions: %s", errOut)
	assert.Contains(t, out, "LAST USED", "user sessions table")
	_, out, _ = c.run("user", "sessions", "2")
	assert.Contains(t, out, "user 2 has no sessions")

	code, out, errOut = c.run("user", "reset", "1", "--scope", "password")
	require.Equal(t, 0, code, "user reset: %s", errOut)
	assert.Contains(t, out, "password reset for user 1 created as invite 2", "user reset")
	assert.Contains(t, out, "/reset#token=", "user reset prints the link")
	code, _, errOut = c.run("user", "reset", "1")
	assert.Equal(t, 2, code, "user reset without --scope")
	assert.Contains(t, errOut, "give --scope password|totp|both")
	code, _, errOut = c.run("user", "reset", "99", "--scope", "both")
	assert.Equal(t, 1, code, "user reset of a missing account")
	assert.Contains(t, errOut, "Not Found")

	_, out, _ = c.run("invite", "list")
	assert.Contains(t, out, "signup", "invite list")
	assert.Contains(t, out, "password", "invite list shows a reset's scope")
	assert.NotContains(t, out, "#token=", "invite list never shows a link")

	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"invite", "revoke", "1"}, "invite 1 revoked"},
		{[]string{"session", "revoke", "1"}, "session 1 revoked"},
		{[]string{"user", "disable", "2"}, "user 2 disabled"},
		{[]string{"user", "enable", "2"}, "user 2 enabled"},
		{[]string{"user", "delete", "2"}, "user 2 deleted"},
		{[]string{"auth", "rotate-key"}, "signing key rotated"},
	} {
		code, out, errOut := c.run(step.args...)
		require.Equal(t, 0, code, "%v: %s %s", step.args, out, errOut)
		assert.Contains(t, out, step.want, "%v", step.args)
	}
	_, out, _ = c.run("user", "list")
	assert.NotContains(t, out, "fixture_other", "a deleted account is gone")
	code, _, errOut = c.run("user", "delete", "2")
	assert.Equal(t, 1, code, "delete of a deleted account")
	assert.Contains(t, errOut, "Not Found")

	for _, args := range [][]string{{"user", "bogus"}, {"user", "disable"}, {"user", "disable", "x"}, {"auth", "rotate-key", "now"}} {
		code, _, _ := c.run(args...)
		assert.Equal(t, 2, code, "%v is a usage error", args)
	}
	code, out, _ = c.run("invite")
	assert.Equal(t, 0, code, "bare invite prints help")
	assert.Contains(t, out, "user reset ID --scope", "help lists the commands")
}
