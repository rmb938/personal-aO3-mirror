package controllers

import (
	"bytes"
	"context"
	"encoding/base32"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// cheapArgon2 keeps the tests fast; the parameters don't change any behaviour under test.
var cheapArgon2 = auth.Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

// Fixture passwords (they pass the policy).
const (
	testPassword  = "fixture correct horse battery"
	testPassword2 = "fixture new staple battery" //nolint:gosec // G101: a test fixture, not a credential
)

type authEnv struct {
	a     *Auth
	mk    *MasterKeys
	opts  AuthOptions
	clk   *fakeClock
	users *store.Users
	keys  *store.Keys
	logs  *bytes.Buffer
}

// newAuthEnv builds an Auth over temp stores; each tweak adjusts its options first.
func newAuthEnv(t *testing.T, tweaks ...func(*AuthOptions)) *authEnv {
	t.Helper()
	dir := t.TempDir()
	clk := &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)} // on a TOTP step boundary
	users, err := store.OpenUsers(context.Background(), filepath.Join(dir, "api"), store.Options{Now: clk.Now})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, users.Close()) })
	keys, err := store.OpenKeys(context.Background(), filepath.Join(dir, "api"), store.Options{Now: clk.Now})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keys.Close()) })
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mk, err := NewMasterKeys(context.Background(), MasterKeysOptions{Keys: keys, Users: users, Now: clk.Now, Logger: log})
	require.NoError(t, err)
	hasher, err := auth.NewHasher(mk.Keyring(), auth.HasherOptions{Params: cheapArgon2})
	require.NoError(t, err)
	opts := AuthOptions{Users: users, MasterKeys: mk, Hasher: hasher, Now: clk.Now, Logger: log,
		PublicURL: "https://mirror.example.com"}
	for _, f := range tweaks {
		f(&opts)
	}
	a, err := NewAuth(context.Background(), opts)
	require.NoError(t, err)
	return &authEnv{a: a, mk: mk, opts: opts, clk: clk, users: users, keys: keys, logs: logs}
}

func (e *authEnv) advance(d time.Duration) { _ = e.clk.Sleep(context.Background(), d) }

// nextStep moves the clock to the start of the next TOTP step, so a fresh code is accepted.
func (e *authEnv) nextStep() {
	now := e.clk.Now()
	e.advance(now.Truncate(auth.TOTPPeriod).Add(auth.TOTPPeriod).Sub(now))
}

func inviteToken(t *testing.T, l InviteLink) string {
	t.Helper()
	_, tok, ok := strings.Cut(l.URL, "#token=")
	require.True(t, ok, "invite URL has a token")
	return tok
}

func (e *authEnv) invite(t *testing.T) string {
	t.Helper()
	l, err := e.a.CreateInvite(context.Background(), 0)
	require.NoError(t, err)
	return inviteToken(t, l)
}

func decodeSecret(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	require.NoError(t, err)
	return b
}

type testUser struct {
	id     int64
	name   string
	secret []byte
	codes  []string
}

// signup creates an account through both signup steps.
func (e *authEnv) signup(t *testing.T, name string) testUser {
	t.Helper()
	ctx := context.Background()
	en, err := e.a.Signup(ctx, e.invite(t), name, testPassword)
	require.NoError(t, err)
	secret := decodeSecret(t, en.TOTPSecret)
	c, err := e.a.ConfirmSignup(ctx, en.Token, auth.TOTPCode(secret, e.clk.Now()))
	require.NoError(t, err)
	return testUser{id: c.UserID, name: name, secret: secret, codes: c.RecoveryCodes}
}

// login runs both login steps with a fresh TOTP code.
func (e *authEnv) login(t *testing.T, u testUser) TokenPair {
	t.Helper()
	ctx := context.Background()
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	e.nextStep()
	p, err := e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.NoError(t, err)
	return p
}

func TestAuthLoginRefreshLogout(t *testing.T) {
	// A cap above the idle limit, so the sliding expiry shows (TestAuthSessionMaxAge has the cap).
	e := newAuthEnv(t, func(o *AuthOptions) { o.MaxSessionAge = 2160 * time.Hour })
	ctx := context.Background()
	u := e.signup(t, "Fixture_User")
	assert.Len(t, u.codes, auth.RecoveryCodeCount)

	// The username is stored lower-cased and logs in in any case.
	u.name = "FIXTURE_user"
	p := e.login(t, u)
	assert.Equal(t, u.id, p.UserID)
	assert.Equal(t, 15*time.Minute, p.AccessTTL)
	assert.Equal(t, e.clk.Now().Add(720*time.Hour), p.RefreshExpiresAt)
	got, err := e.a.Authenticate(ctx, p.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, Principal{UserID: u.id, SessionID: p.SessionID}, got)

	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Equal(t, "fixture_user", acct.Username)
	assert.Equal(t, e.clk.Now(), acct.LastLoginAt.UTC())

	// Refresh rotates: the new token works once, the session is the same.
	e.advance(time.Hour)
	p2, err := e.a.Refresh(ctx, p.RefreshToken)
	require.NoError(t, err)
	assert.Equal(t, p.SessionID, p2.SessionID)
	assert.NotEqual(t, p.RefreshToken, p2.RefreshToken)
	assert.Equal(t, e.clk.Now().Add(720*time.Hour), p2.RefreshExpiresAt, "sliding expiry")
	_, err = e.a.Authenticate(ctx, p2.AccessToken)
	require.NoError(t, err)

	// Logout revokes the session: both access tokens stop at once.
	require.NoError(t, e.a.Logout(ctx, got))
	_, err = e.a.Authenticate(ctx, p2.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = e.a.Refresh(ctx, p2.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthRefreshReuseRevokes(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	p := e.login(t, e.signup(t, "fixture_user"))
	p2, err := e.a.Refresh(ctx, p.RefreshToken)
	require.NoError(t, err)

	// The old refresh token again (a copy): refused, and the whole session is revoked.
	_, err = e.a.Refresh(ctx, p.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = e.a.Authenticate(ctx, p2.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = e.a.Refresh(ctx, p2.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	assert.Contains(t, e.logs.String(), `"reason":"refresh_reuse"`)

	for _, bad := range []string{"", "rt_short", "chl_" + strings.Repeat("A", 43), p.AccessToken} {
		_, err = e.a.Refresh(ctx, bad)
		require.ErrorIs(t, err, ErrInvalidToken)
	}
}

func TestAuthRefreshExpires(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	p := e.login(t, e.signup(t, "fixture_user"))
	e.advance(720 * time.Hour)
	_, err := e.a.Refresh(ctx, p.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthSessionMaxAge(t *testing.T) {
	// A cap above the 30d idle limit (the default sets both to 30d), so refreshes extend.
	e := newAuthEnv(t, func(o *AuthOptions) { o.MaxSessionAge = 2160 * time.Hour })
	ctx := context.Background()
	p := e.login(t, e.signup(t, "fixture_user"))
	loggedIn := e.clk.Now()
	maxAge := loggedIn.Add(2160 * time.Hour)

	// Refreshed well within the idle limit every time, the session still ends at the cap.
	for e.clk.Now().Add(29 * 24 * time.Hour).Before(maxAge) {
		e.advance(29 * 24 * time.Hour)
		var err error
		p, err = e.a.Refresh(ctx, p.RefreshToken)
		require.NoError(t, err)
	}
	assert.Equal(t, maxAge, p.RefreshExpiresAt.UTC(), "capped, not now + refresh TTL")
	sessions, err := e.a.UserSessions(ctx, p.UserID)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, maxAge, sessions[0].ExpiresAt.UTC())

	e.advance(maxAge.Sub(e.clk.Now()))
	_, err = e.a.Refresh(ctx, p.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthAccessTokenExpiresAndChecksSession(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	a := e.signup(t, "fixture_a")
	b := e.signup(t, "fixture_b")
	pa := e.login(t, a)
	pb := e.login(t, b)

	// A validly signed token naming B's session as A's is refused.
	forged, _, err := e.a.keys().issuer.Issue(a.id, pb.SessionID)
	require.NoError(t, err)
	_, err = e.a.Authenticate(ctx, forged)
	require.ErrorIs(t, err, ErrInvalidToken)
	assert.Contains(t, e.logs.String(), `"reason":"session_user_mismatch"`)

	e.advance(15*time.Minute + auth.JWTLeeway + time.Second)
	_, err = e.a.Authenticate(ctx, pa.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	var te *auth.TokenError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, auth.TokenExpired, te.Reason)
}

func TestAuthTOTPReplayRejected(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	// The signup confirm used this step's code: it can't log in.
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.ErrorIs(t, err, ErrInvalidCredentials)

	e.advance(time.Minute) // past the backoff, and a new step
	code := auth.TOTPCode(u.secret, e.clk.Now())
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: code})
	require.NoError(t, err)

	// The same code in the same step, on a new challenge: replay.
	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: code})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthChallengeLimits(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	for i := range MaxChallengeAttempts {
		_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: "000000"})
		require.ErrorIs(t, err, ErrInvalidCredentials, "attempt %d", i+1)
		e.advance(backoffDelay(i + 1))
	}
	e.nextStep()
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.ErrorIs(t, err, ErrInvalidToken, "out of attempts")

	// A challenge expires after ChallengeTTL.
	e.advance(backoffMax)
	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	e.advance(ChallengeTTL)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.ErrorIs(t, err, ErrInvalidToken)

	// Both factors at once, or neither, never passes.
	e.advance(backoffMax)
	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	e.nextStep()
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now()), RecoveryCode: u.codes[0]})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthRecoveryCodeLogin(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: strings.ToUpper(u.codes[0])})
	require.NoError(t, err)
	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Equal(t, auth.RecoveryCodeCount-1, acct.RecoveryCodesLeft)

	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[0]})
	require.ErrorIs(t, err, ErrInvalidCredentials, "single use")

	// A well-formed code that was never issued, and another user's code.
	other := e.signup(t, "fixture_other")
	for name, code := range map[string]string{"wrong": auth.NewRecoveryCodes()[0], "another user's": other.codes[0]} {
		e.advance(backoffMax)
		ch, err = e.a.Login(ctx, u.name, testPassword)
		require.NoError(t, err)
		_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: code})
		require.ErrorIs(t, err, ErrInvalidCredentials, name)
	}
	acct, err = e.a.Account(ctx, other.id)
	require.NoError(t, err)
	assert.Equal(t, auth.RecoveryCodeCount, acct.RecoveryCodesLeft, "another user's code stays unused")

	// The last code still works after the failures: every stored code is checked, not only the first.
	e.advance(backoffMax)
	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[auth.RecoveryCodeCount-1]})
	require.NoError(t, err)
}

// Codes sealed under another keyring (a users.db paired with the wrong keys.db) are a server
// error, never a login and never "wrong code".
func TestAuthRecoveryCodesUnderAnotherKeyring(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	ring := e.mk.Keyring().Load()
	otherRing, err := auth.NewKeyring(ring.ActiveID(), []auth.MasterKey{{ID: ring.ActiveID(), Key: auth.NewMasterKey()}})
	require.NoError(t, err)
	norm, ok := auth.NormalizeRecoveryCode(u.codes[0])
	require.True(t, ok)
	require.NoError(t, e.users.AutoCommitForTestOnly().ReplaceRecoveryCodes(ctx, u.id,
		[][]byte{otherRing.EncryptRecoveryCode(u.id, norm)}))

	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[0]})
	require.ErrorIs(t, err, auth.ErrDecrypt)
}

// The stored codes are sealed digests: no code appears in users.db, and each opens only for its user.
func TestAuthRecoveryCodesStoredSealed(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	rows, err := e.users.AutoCommitForTestOnly().UnusedRecoveryCodes(ctx, u.id)
	require.NoError(t, err)
	require.Len(t, rows, auth.RecoveryCodeCount)
	encs := make([][]byte, len(rows))
	for i, row := range rows {
		encs[i] = row.CodeEnc
		for _, c := range u.codes {
			norm, _ := auth.NormalizeRecoveryCode(c)
			assert.False(t, bytes.Contains(row.CodeEnc, []byte(norm)), "no code in the clear")
		}
	}
	ring := e.mk.Keyring().Load()
	norm, _ := auth.NormalizeRecoveryCode(u.codes[3])
	i, err := ring.MatchRecoveryCode(u.id, norm, encs)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, i, 0)
	_, err = ring.MatchRecoveryCode(u.id+1, norm, encs)
	require.ErrorIs(t, err, auth.ErrDecrypt, "bound to the user")
}

func TestAuthKeyRotation(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	p := e.login(t, u)
	oldKey := e.a.ring.Load().active

	kid, err := e.a.RotateSigningKey(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, oldKey.ID, kid)
	_, err = e.a.Authenticate(ctx, p.AccessToken)
	require.NoError(t, err, "the old key verifies until its tokens expire")
	p2, err := e.a.Refresh(ctx, p.RefreshToken)
	require.NoError(t, err)
	_, err = e.a.Authenticate(ctx, p2.AccessToken)
	require.NoError(t, err)

	// A restart keeps both keys (the old one still in its grace period).
	e.a, err = NewAuth(ctx, e.opts)
	require.NoError(t, err)
	assert.Equal(t, kid, e.a.ring.Load().active.ID)
	_, err = e.a.Authenticate(ctx, p.AccessToken)
	require.NoError(t, err)

	// After the grace period the old key is gone, even for a token that hasn't expired.
	longLived := auth.NewIssuer(e.opts.PublicURL, oldKey, 24*time.Hour, e.clk.Now)
	e.advance(15*time.Minute + auth.JWTLeeway)
	tok, _, err := longLived.Issue(u.id, p.SessionID)
	require.NoError(t, err)
	_, err = e.a.Authenticate(ctx, tok)
	var te *auth.TokenError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, auth.TokenUnknownKey, te.Reason)
	p3, err := e.a.Refresh(ctx, p2.RefreshToken)
	require.NoError(t, err)
	_, err = e.a.Authenticate(ctx, p3.AccessToken)
	require.NoError(t, err, "the new key still signs and verifies")

	// And a restart then deletes it.
	_, err = NewAuth(ctx, e.opts)
	require.NoError(t, err)
	keys, err := e.users.AutoCommitForTestOnly().SigningKeys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, kid, keys[0].ID)
}

func TestAuthSigningSeedsEncrypted(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	_, err := e.a.RotateSigningKey(ctx) // one retired and one active row
	require.NoError(t, err)
	r := e.a.ring.Load()
	seeds := map[string][]byte{r.active.ID: r.active.Seed()}

	ring := e.mk.Keyring().Load()
	otherRing, err := auth.NewKeyring(ring.ActiveID(), []auth.MasterKey{{ID: ring.ActiveID(), Key: auth.NewMasterKey()}})
	require.NoError(t, err)
	other, err := auth.NewLiveKeyring(otherRing)
	require.NoError(t, err)
	rows, err := e.users.AutoCommitForTestOnly().SigningKeys(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		seed, err := ring.DecryptSigningSeed(row.ID, row.SeedEnc)
		require.NoError(t, err)
		require.Len(t, seed, 32)
		assert.False(t, bytes.Contains(row.SeedEnc, seed), "no seed bytes in the clear")
		if want, ok := seeds[row.ID]; ok {
			assert.Equal(t, want, seed)
		}
		_, err = otherRing.DecryptSigningSeed(row.ID, row.SeedEnc)
		require.ErrorIs(t, err, auth.ErrDecrypt, "another keyring with the same key ID")
	}

	// A restart loads the same keys back; under another keyring it refuses to start.
	again, err := NewAuth(ctx, e.opts)
	require.NoError(t, err)
	assert.Equal(t, r.active.ID, again.ring.Load().active.ID)
	assert.Equal(t, r.active.Seed(), again.ring.Load().active.Seed())
	opts := e.opts
	opts.MasterKeys = &MasterKeys{live: other}
	_, err = NewAuth(ctx, opts)
	require.ErrorIs(t, err, auth.ErrDecrypt)
}

func TestAuthScheduledKeyRotation(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	first := e.a.ring.Load().active.ID
	rotateIfDue := func() bool {
		t.Helper()
		rotated, err := e.a.RotateSigningKeyIfDue(ctx)
		require.NoError(t, err)
		return rotated
	}
	storedKeys := func() int {
		t.Helper()
		keys, err := e.users.AutoCommitForTestOnly().SigningKeys(ctx)
		require.NoError(t, err)
		return len(keys)
	}

	e.advance(720*time.Hour - time.Second)
	assert.False(t, rotateIfDue(), "not due yet")
	assert.Equal(t, first, e.a.ring.Load().active.ID)

	// A restart keeps the key's age (created_at comes from users.db).
	var err error
	e.a, err = NewAuth(ctx, e.opts)
	require.NoError(t, err)
	assert.False(t, rotateIfDue())

	p := e.login(t, u)
	e.advance(time.Second)
	assert.True(t, rotateIfDue(), "due at exactly auth.signing_key_rotation")
	second := e.a.ring.Load().active.ID
	assert.NotEqual(t, first, second)
	assert.False(t, rotateIfDue(), "due twice in a row rotates once")
	assert.Equal(t, second, e.a.ring.Load().active.ID)
	assert.Equal(t, 2, storedKeys())
	_, err = e.a.Authenticate(ctx, p.AccessToken)
	require.NoError(t, err, "the old key verifies until its tokens expire")
	assert.Contains(t, e.logs.String(), `"event":"signing_key_rotated","reason":"scheduled"`)

	// The re-check inside the transaction: a stale ring whose key users.db has already
	// replaced (say, by another process) doesn't rotate again.
	e.advance(10 * 24 * time.Hour)
	stale := *e.a.ring.Load()
	stale.created = stale.created.Add(-720 * time.Hour)
	e.a.ring.Store(&stale)
	assert.False(t, rotateIfDue())
	assert.Equal(t, second, e.a.ring.Load().active.ID)

	// A manual rotation resets the age.
	e.advance(10 * 24 * time.Hour)
	_, err = e.a.RotateSigningKey(ctx)
	require.NoError(t, err)
	assert.Contains(t, e.logs.String(), `"event":"signing_key_rotated","reason":"admin"`)
	third := e.a.ring.Load().active.ID
	e.advance(20 * 24 * time.Hour)
	assert.False(t, rotateIfDue(), "30 days since the start, 20 since the manual rotation")
	e.advance(10 * 24 * time.Hour)
	assert.True(t, rotateIfDue())
	assert.NotEqual(t, third, e.a.ring.Load().active.ID)
	assert.Equal(t, 2, storedKeys(), "retired keys past their grace are deleted")
}

func TestAuthHashingRefusedInsideTx(t *testing.T) {
	e := newAuthEnv(t)
	err := e.users.InReadTx(context.Background(), func(ctx context.Context, _ *store.UsersReadTx) error {
		_, err := e.a.hashPassword(ctx, auth.UserPassword(1), testPassword)
		return err
	})
	require.ErrorIs(t, err, store.ErrTxMisuse)
	require.ErrorContains(t, err, "password hash inside a transaction")
}

func TestNewAuthChecksOptions(t *testing.T) {
	e := newAuthEnv(t)
	for _, u := range []string{"", "mirror.example.com", "ftp://mirror.example.com", "https://mirror.example.com/api",
		"https://mirror.example.com/?x=1", "https://user@mirror.example.com", "https://mirror.example.com/#frag"} {
		o := e.opts
		o.PublicURL = u
		_, err := NewAuth(context.Background(), o)
		require.Error(t, err, u)
	}
	o := e.opts
	o.Hasher = nil
	_, err := NewAuth(context.Background(), o)
	require.Error(t, err)
}

// TestAuthLogsAreSecretFree runs every flow with recognisable values and checks no username,
// password, token, code or secret reaches the logs.
func TestAuthLogsAreSecretFree(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	var secrets []string
	keep := func(s ...string) { secrets = append(secrets, s...) }

	l, err := e.a.CreateInvite(ctx, 0)
	require.NoError(t, err)
	keep(l.URL, inviteToken(t, l))
	en, err := e.a.Signup(ctx, inviteToken(t, l), "sentinel_user", testPassword)
	require.NoError(t, err)
	keep(en.Token, en.TOTPSecret, en.OTPAuthURI)
	secret := decodeSecret(t, en.TOTPSecret)
	code := auth.TOTPCode(secret, e.clk.Now())
	c, err := e.a.ConfirmSignup(ctx, en.Token, code)
	require.NoError(t, err)
	keep(code)
	keep(c.RecoveryCodes...)
	u := testUser{id: c.UserID, name: "sentinel_user", secret: secret, codes: c.RecoveryCodes}

	_, err = e.a.Login(ctx, "sentinel_user", "SENTINEL wrong password")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = e.a.Login(ctx, "sentinel_nobody", "SENTINEL wrong password")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	e.advance(time.Minute)
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	keep(ch.Token)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: "123456"})
	require.ErrorIs(t, err, ErrInvalidCredentials)
	e.advance(time.Minute)
	p, err := e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[0]})
	require.NoError(t, err)
	keep(p.AccessToken, p.RefreshToken)
	p2, err := e.a.Refresh(ctx, p.RefreshToken)
	require.NoError(t, err)
	keep(p2.AccessToken, p2.RefreshToken)
	_, err = e.a.Refresh(ctx, p.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	r, err := e.a.CreateReset(ctx, u.id, model.ResetBoth, time.Hour)
	require.NoError(t, err)
	keep(r.URL, inviteToken(t, r))
	en, err = e.a.StartReset(ctx, inviteToken(t, r), "SENTINEL new password ok")
	require.NoError(t, err)
	keep(en.Token, en.TOTPSecret, en.OTPAuthURI)
	_, err = e.a.RotateSigningKey(ctx)
	require.NoError(t, err)

	logs := e.logs.String()
	require.NotEmpty(t, logs)
	for _, s := range append(secrets, "sentinel", "SENTINEL", testPassword) {
		assert.NotContains(t, logs, s)
	}
}
