package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Auth runs the API's account operations over users.db (docs/part2-api.md stage 16): invites,
// signup and reset, the two-step login, sessions with rotating refresh tokens, access-token
// signing keys, and the admin operations. It is safe to share.
//
// Files: auth.go (this: options, errors, keys, Authenticate), auth_backoff.go (lockout),
// auth_login.go (login, refresh, logout), auth_enroll.go (signup, reset), auth_account.go (a
// user's own account), auth_admin.go (the admin socket's operations).
//
// Password hashing runs between transactions (read → hash → write), never inside one (checked
// at runtime with store.NotInTx). Logs carry an event, a reason and numeric IDs; never
// usernames, passwords, codes or tokens.
type Auth struct {
	opts  AuthOptions
	users *store.Users
	log   *slog.Logger
	names *nameBackoff

	keysMu sync.Mutex // serializes rotation and ring rebuilds
	ring   atomic.Pointer[keyRing]
}

// AuthOptions configure NewAuth. Zero durations take the defaults.
type AuthOptions struct {
	Users   *store.Users
	Keyring *auth.Keyring // from LoadKeyring
	Hasher  *auth.Hasher
	Logger  *slog.Logger
	Now     func() time.Time

	PublicURL  string        // api.public_url: the JWT iss and the base of invite links
	AccessTTL  time.Duration // auth.access_ttl; 15m
	RefreshTTL time.Duration // auth.refresh_ttl, the idle limit (each refresh extends by it); 720h
	// MaxSessionAge is auth.max_session_age: a session ends this long after its login however
	// often it is refreshed; 720h (NIST SP 800-63B-4 §2.1.3: an AAL1 overall timeout SHOULD
	// be no more than 30 days).
	MaxSessionAge time.Duration
	InviteTTL     time.Duration // auth.invite_ttl, the default for new invites; 24h
	TOTPIssuer    string        // auth.totp_issuer; "ao3mirror"
	// SigningKeyRotation is auth.signing_key_rotation: RotateSigningKeyIfDue replaces a signing
	// key this old; 720h.
	SigningKeyRotation time.Duration

	UnknownNames int // unknown usernames whose backoff is remembered; 10000
}

// Auth limits (docs/part2-api.md stage 16 "Lockout", "Invites").
const (
	MaxInviteTTL         = 7 * 24 * time.Hour
	MinInviteTTL         = time.Minute
	MaxFailedAttempts    = 100 // consecutive failures that disable an account
	MaxChallengeAttempts = 5   // second-factor tries per login challenge
	ChallengeTTL         = 5 * time.Minute
	EnrollmentTTL        = 15 * time.Minute // capped by the invite's own expiry
)

var (
	// ErrInvalidCredentials is a failed login or re-authentication: an unknown user, a wrong
	// password, TOTP code or recovery code, or a disabled account, all alike.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrInvalidToken is an opaque or access token that is malformed, unknown, expired, used
	// or revoked.
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrInvalidUsername is a username outside [a-z0-9_-]{3,32} (after lower-casing ASCII).
	ErrInvalidUsername = errors.New("auth: invalid username")
	// ErrUsernameTaken is a signup whose username an account already has.
	ErrUsernameTaken = errors.New("auth: username taken")
	// ErrResetPassword is a reset whose password presence doesn't match its scope: a password is
	// required exactly when the scope replaces it.
	ErrResetPassword = errors.New("auth: password does not match the reset scope")
	// ErrResetScope is an unknown reset scope.
	ErrResetScope = errors.New("auth: unknown reset scope")
	// ErrInviteTTL is an invite TTL outside MinInviteTTL..MaxInviteTTL.
	ErrInviteTTL = errors.New("auth: invite ttl out of range")
)

// RateLimitedError is an attempt refused by the account's backoff (or an unknown username's,
// which behaves the same). The API answers 429 with Retry-After.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e RateLimitedError) Error() string {
	return fmt.Sprintf("auth: rate limited; retry after %s", e.RetryAfter)
}

// Principal is who an access token speaks for.
type Principal struct {
	UserID    int64
	SessionID int64
}

// NewAuth returns the Auth controller. It loads the signing keys from users.db, creating the
// first one, and drops keys whose tokens have all expired.
func NewAuth(ctx context.Context, opts AuthOptions) (*Auth, error) {
	if opts.Users == nil || opts.Keyring == nil || opts.Hasher == nil {
		return nil, errors.New("controllers: auth needs users, a keyring and a hasher")
	}
	if err := checkPublicURL(opts.PublicURL); err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.AccessTTL = durationOr(opts.AccessTTL, 15*time.Minute)
	opts.RefreshTTL = durationOr(opts.RefreshTTL, 720*time.Hour)
	opts.MaxSessionAge = durationOr(opts.MaxSessionAge, 720*time.Hour)
	opts.InviteTTL = durationOr(opts.InviteTTL, 24*time.Hour)
	opts.SigningKeyRotation = durationOr(opts.SigningKeyRotation, 720*time.Hour)
	if opts.TOTPIssuer == "" {
		opts.TOTPIssuer = "ao3mirror"
	}
	if opts.UnknownNames == 0 {
		opts.UnknownNames = 10000
	}
	a := &Auth{opts: opts, users: opts.Users, log: opts.Logger.With("component", "auth"),
		names: newNameBackoff(opts.UnknownNames)}
	if err := a.loadKeys(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func durationOr(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// checkPublicURL accepts an http(s) origin: scheme and host, no path beyond "/", no query,
// fragment or user info.
func checkPublicURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("controllers: auth public URL must be an http(s) origin with no path")
	}
	return nil
}

func (a *Auth) now() time.Time { return a.opts.Now() }

// event logs an auth event: what happened and content-free attrs (IDs, an enum reason).
func (a *Auth) event(ctx context.Context, level slog.Level, event string, attrs ...any) {
	logx.From(ctx, a.log).Log(ctx, level, "auth event", append([]any{"event", event}, attrs...)...)
}

// Reasons in auth event logs (a closed set).
const (
	reasonUnknownUser    = "unknown_user"
	reasonBadPassword    = "bad_password"
	reasonDisabled       = "disabled"
	reasonBackoff        = "backoff"
	reasonBadCode        = "bad_code"
	reasonUnknownToken   = "unknown_token"
	reasonExpired        = "expired"
	reasonSpent          = "spent" // used, revoked, or out of attempts
	reasonChanged        = "changed_concurrently"
	reasonWrongKind      = "wrong_kind"
	reasonRefreshReuse   = "refresh_reuse"
	reasonSessionUser    = "session_user_mismatch"
	reasonSessionRevoked = "session_inactive"
	reasonAdmin          = "admin"     // an admin asked (signing key rotation)
	reasonScheduled      = "scheduled" // the key reached auth.signing_key_rotation
)

// ---- passwords (never inside a transaction) ----

// notHashingInTx is the NotInTx reason for all three.
const notHashingInTx = "password hash"

func (a *Auth) verifyPassword(ctx context.Context, phc, password string) (bool, error) {
	if err := store.NotInTx(ctx, notHashingInTx); err != nil {
		return false, err
	}
	return a.opts.Hasher.Verify(ctx, phc, password)
}

func (a *Auth) verifyDummy(ctx context.Context, password string) error {
	if err := store.NotInTx(ctx, notHashingInTx); err != nil {
		return err
	}
	return a.opts.Hasher.VerifyDummy(ctx, password)
}

func (a *Auth) hashPassword(ctx context.Context, password string) (string, error) {
	if err := store.NotInTx(ctx, notHashingInTx); err != nil {
		return "", err
	}
	return a.opts.Hasher.Hash(ctx, password)
}

// NormalizeUsername returns the stored form of a username: ASCII letters lower-cased. It is
// false unless the result is 3 to 32 of [a-z0-9_-] (so non-ASCII is refused, never folded).
func NormalizeUsername(s string) (string, bool) {
	if len(s) < 3 || len(s) > 32 {
		return s, false
	}
	b := []byte(s)
	for i, c := range b {
		switch {
		case 'A' <= c && c <= 'Z':
			b[i] = c + ('a' - 'A')
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '_', c == '-':
		default:
			return s, false
		}
	}
	return string(b), true
}

// ---- second factor ----

// SecondFactor is a TOTP code or a recovery code; exactly one is set.
type SecondFactor struct {
	Code         string
	RecoveryCode string
}

// checkSecondFactor checks a user's TOTP code (against replay) or recovery code (marking it
// used). step is the accepted TOTP step, 0 for a recovery code.
func (a *Auth) checkSecondFactor(ctx context.Context, tx *store.UsersTx, u model.User, f SecondFactor, now time.Time) (step int64, ok bool, err error) {
	switch {
	case f.Code != "" && f.RecoveryCode == "":
		secret, err := a.opts.Keyring.DecryptTOTPSecret(auth.UserTOTP(u.ID), u.TOTPSecretEnc)
		if err != nil {
			return 0, false, err
		}
		step, ok := auth.VerifyTOTP(secret, f.Code, now, u.LastTOTPStep)
		return step, ok, nil
	case f.RecoveryCode != "" && f.Code == "":
		norm, ok := auth.NormalizeRecoveryCode(f.RecoveryCode)
		if !ok {
			return 0, false, nil
		}
		used, err := tx.UseRecoveryCode(ctx, u.ID, a.opts.Keyring.RecoveryCodeMAC(norm), now)
		return 0, used, err
	}
	return 0, false, nil
}

// newRecoveryCodes makes a fresh set of codes for a user, replacing the old ones.
func (a *Auth) newRecoveryCodes(ctx context.Context, tx *store.UsersTx, userID int64) ([]string, error) {
	codes := auth.NewRecoveryCodes()
	macs := make([][32]byte, len(codes))
	for i, c := range codes {
		norm, _ := auth.NormalizeRecoveryCode(c)
		macs[i] = a.opts.Keyring.RecoveryCodeMAC(norm)
	}
	return codes, tx.ReplaceRecoveryCodes(ctx, userID, macs)
}

// ---- signing keys ----

// keyRing is the immutable signing state: the active key's Issuer and a Verifier over it and
// the retired keys still in their grace period. It is replaced, never changed.
type keyRing struct {
	active   auth.SigningKey
	created  time.Time // the active key's created_at
	retired  []retiredKey
	issuer   *auth.Issuer
	verifier *auth.Verifier
	nextDrop time.Time // when the first retired key leaves; zero = none
}

type retiredKey struct {
	key   auth.VerifyKey
	until time.Time
}

// keyGrace is how long a retired key keeps verifying: every token it signed has expired by
// then (exp ≤ rotation + access TTL, plus the verifier's leeway).
func (a *Auth) keyGrace() time.Duration { return a.opts.AccessTTL + auth.JWTLeeway }

func (a *Auth) newRing(active auth.SigningKey, created time.Time, retired []retiredKey, now time.Time) *keyRing {
	r := &keyRing{active: active, created: created, issuer: auth.NewIssuer(a.opts.PublicURL, active, a.opts.AccessTTL, a.opts.Now)}
	keys := []auth.VerifyKey{active.VerifyKey()}
	for _, k := range retired {
		if !now.Before(k.until) {
			continue
		}
		r.retired = append(r.retired, k)
		keys = append(keys, k.key)
		if r.nextDrop.IsZero() || k.until.Before(r.nextDrop) {
			r.nextDrop = k.until
		}
	}
	r.verifier = auth.NewVerifier(a.opts.PublicURL, keys, a.opts.Now)
	return r
}

// keys returns the current ring, first dropping retired keys whose grace has ended.
func (a *Auth) keys() *keyRing {
	r := a.ring.Load()
	now := a.now()
	if r.nextDrop.IsZero() || now.Before(r.nextDrop) {
		return r
	}
	a.keysMu.Lock()
	defer a.keysMu.Unlock()
	r = a.ring.Load()
	if !r.nextDrop.IsZero() && !now.Before(r.nextDrop) {
		r = a.newRing(r.active, r.created, r.retired, now)
		a.ring.Store(r)
	}
	return r
}

// loadKeys reads the signing keys, deleting those past their grace and creating the first.
func (a *Auth) loadKeys(ctx context.Context) error {
	now := a.now()
	rows, err := store.WriteUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersTx) ([]model.SigningKey, error) {
		if err := tx.DeleteSigningKeysRetiredBy(ctx, now.Add(-a.keyGrace())); err != nil {
			return nil, err
		}
		rows, err := tx.SigningKeys(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.RetiredAt.IsZero() {
				return rows, nil
			}
		}
		k := auth.NewSigningKey()
		row := model.SigningKey{ID: k.ID, Seed: k.Seed(), CreatedAt: now}
		return append(rows, row), tx.AddSigningKey(ctx, row)
	})
	if err != nil {
		return err
	}
	var active *auth.SigningKey
	var created time.Time
	var retired []retiredKey
	for _, row := range rows { // oldest first: the newest active key signs
		k, err := auth.SigningKeyFromSeed(row.ID, row.Seed)
		if err != nil {
			return fmt.Errorf("controllers: signing key: %w", err)
		}
		if row.RetiredAt.IsZero() {
			if active != nil { // more than one active (never written so): the older only verifies
				retired = append(retired, retiredKey{key: active.VerifyKey(), until: now.Add(a.keyGrace())})
			}
			active, created = &k, row.CreatedAt
			continue
		}
		retired = append(retired, retiredKey{key: k.VerifyKey(), until: row.RetiredAt.Add(a.keyGrace())})
	}
	a.ring.Store(a.newRing(*active, created, retired, now))
	return nil
}

// RotateSigningKey retires the signing key and starts a new one at once (an admin's request).
// Tokens signed by the old key keep verifying until they expire. It returns the new key's kid.
func (a *Auth) RotateSigningKey(ctx context.Context) (string, error) {
	kid, _, err := a.rotateSigningKey(ctx, false)
	return kid, err
}

// RotateSigningKeyIfDue rotates the signing key once it is auth.signing_key_rotation old, so a
// leaked key stops working without an admin acting. serve calls it at startup and then hourly.
// It is false when the key isn't due yet.
func (a *Auth) RotateSigningKeyIfDue(ctx context.Context) (bool, error) {
	if !a.signingKeyDue(a.keys().created, a.now()) {
		return false, nil // the common case, without the lock or a transaction
	}
	_, rotated, err := a.rotateSigningKey(ctx, true)
	return rotated, err
}

func (a *Auth) signingKeyDue(created, now time.Time) bool {
	return !now.Before(created.Add(a.opts.SigningKeyRotation))
}

// rotateSigningKey replaces the active signing key in one transaction. With ifDue it first
// re-checks, inside that transaction, that the stored active key is due, so two triggers at
// once rotate only once.
func (a *Auth) rotateSigningKey(ctx context.Context, ifDue bool) (kid string, rotated bool, err error) {
	a.keysMu.Lock()
	defer a.keysMu.Unlock()
	now := a.now()
	k := auth.NewSigningKey()
	rotated, err = store.WriteUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersTx) (bool, error) {
		if ifDue {
			due, err := a.storedKeyDue(ctx, tx, now)
			if err != nil || !due {
				return false, err
			}
		}
		if err := tx.RetireSigningKeys(ctx, now); err != nil {
			return false, err
		}
		if err := tx.DeleteSigningKeysRetiredBy(ctx, now.Add(-a.keyGrace())); err != nil {
			return false, err
		}
		return true, tx.AddSigningKey(ctx, model.SigningKey{ID: k.ID, Seed: k.Seed(), CreatedAt: now})
	})
	if err != nil || !rotated {
		return "", false, err
	}
	old := a.ring.Load()
	retired := append(slices.Clip(old.retired), retiredKey{key: old.active.VerifyKey(), until: now.Add(a.keyGrace())})
	a.ring.Store(a.newRing(k, now, retired, now))
	reason := reasonAdmin
	if ifDue {
		reason = reasonScheduled
	}
	a.event(ctx, slog.LevelInfo, "signing_key_rotated", "reason", reason)
	return k.ID, true, nil
}

// storedKeyDue reports whether users.db's active signing key (the newest unretired one) has
// reached auth.signing_key_rotation; true when there is none.
func (a *Auth) storedKeyDue(ctx context.Context, tx *store.UsersTx, now time.Time) (bool, error) {
	rows, err := tx.SigningKeys(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range slices.Backward(rows) { // stored oldest first
		if row.RetiredAt.IsZero() {
			return a.signingKeyDue(row.CreatedAt, now), nil
		}
	}
	return true, nil
}

// ---- access tokens ----

// Authenticate checks an access token and its session: the JWT (signature, claims), then the
// session (the token's user's, not revoked or expired) and the user (not disabled), so
// revocation is immediate. Every refusal is ErrInvalidToken (joined with the *auth.TokenError
// for a bad JWT, whose Reason is safe to log).
func (a *Auth) Authenticate(ctx context.Context, token string) (Principal, error) {
	claims, err := a.keys().verifier.Verify(token)
	if err != nil {
		return Principal{}, errors.Join(ErrInvalidToken, err)
	}
	now := a.now()
	reason, err := store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) (string, error) {
		s, err := tx.SessionByID(ctx, claims.SessionID)
		if errors.Is(err, store.ErrNoSession) {
			return reasonUnknownToken, nil
		} else if err != nil {
			return "", err
		}
		if s.UserID != claims.UserID {
			return reasonSessionUser, nil
		}
		if !s.Active(now) {
			return reasonSessionRevoked, nil
		}
		u, err := tx.UserByID(ctx, s.UserID)
		if errors.Is(err, store.ErrNoUser) {
			return reasonUnknownUser, nil
		} else if err != nil {
			return "", err
		}
		if u.Disabled() {
			return reasonDisabled, nil
		}
		return "", nil
	})
	if err != nil {
		return Principal{}, err
	}
	if reason != "" {
		a.event(ctx, slog.LevelDebug, "access_refused", "reason", reason, "user_id", claims.UserID,
			"session_id", claims.SessionID)
		return Principal{}, ErrInvalidToken
	}
	return Principal{UserID: claims.UserID, SessionID: claims.SessionID}, nil
}
