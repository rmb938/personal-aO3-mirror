package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

var (
	// ErrNoChallenge is a challenge token with no pending login.
	ErrNoChallenge = errors.New("store: no such login challenge")
	// ErrNoSession is a session ID or refresh token with no session (or, with a user ID, none
	// of that user's).
	ErrNoSession = errors.New("store: no such session")
)

// ---- login challenges ----

// CreateChallenge stores a pending login under its token's hash.
func (tx *UsersTx) CreateChallenge(ctx context.Context, tokenSum [32]byte, c model.LoginChallenge) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO login_challenges (token_sha256, user_id, expires_at, attempts)
		VALUES (?,?,?,?)`, tokenSum[:], c.UserID, zeroUnix(c.ExpiresAt), c.Attempts)
	return err
}

// ChallengeByToken returns the pending login whose token hashes to sum, or ErrNoChallenge.
func (tx *UsersReadTx) ChallengeByToken(ctx context.Context, sum [32]byte) (model.LoginChallenge, error) {
	var c model.LoginChallenge
	var expires int64
	err := tx.q.QueryRowContext(ctx, `SELECT user_id, expires_at, attempts FROM login_challenges
		WHERE token_sha256 = ?`, sum[:]).Scan(&c.UserID, &expires, &c.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoChallenge
	}
	c.ExpiresAt = unixTime(expires)
	return c, err
}

// SetChallengeAttempts records a pending login's failed second-factor attempts.
func (tx *UsersTx) SetChallengeAttempts(ctx context.Context, sum [32]byte, attempts int) error {
	return execOne(ctx, tx.q, ErrNoChallenge, `UPDATE login_challenges SET attempts = ? WHERE token_sha256 = ?`,
		attempts, sum[:])
}

// DeleteChallenge deletes a pending login, if it is still there.
func (tx *UsersTx) DeleteChallenge(ctx context.Context, sum [32]byte) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM login_challenges WHERE token_sha256 = ?`, sum[:])
	return err
}

// DeleteExpiredChallenges deletes pending logins that expired at or before now.
func (tx *UsersTx) DeleteExpiredChallenges(ctx context.Context, now time.Time) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM login_challenges WHERE expires_at <= ?`, now.Unix())
	return err
}

// ---- sessions ----

const sessionCols = `id, user_id, created_at, last_used_at, expires_at, revoked_at`

func scanSession(row interface{ Scan(...any) error }) (model.Session, error) {
	var s model.Session
	var created, used, expires, revoked int64
	err := row.Scan(&s.ID, &s.UserID, &created, &used, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNoSession
	}
	s.CreatedAt, s.LastUsedAt, s.ExpiresAt, s.RevokedAt = unixTime(created), unixTime(used), unixTime(expires), unixTime(revoked)
	return s, err
}

// CreateSession starts a session whose refresh token hashes to refreshSum and returns its ID.
func (tx *UsersTx) CreateSession(ctx context.Context, userID int64, refreshSum [32]byte, at, expires time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `INSERT INTO sessions (user_id, refresh_sha256, created_at, last_used_at, expires_at)
		VALUES (?,?,?,?,?)`, userID, refreshSum[:], zeroUnix(at), zeroUnix(at), zeroUnix(expires))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SessionByID returns a session, or ErrNoSession.
func (tx *UsersReadTx) SessionByID(ctx context.Context, id int64) (model.Session, error) {
	return scanSession(tx.q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
}

// SessionByRefresh returns the session whose current refresh token hashes to sum, or
// ErrNoSession.
func (tx *UsersReadTx) SessionByRefresh(ctx context.Context, sum [32]byte) (model.Session, error) {
	return scanSession(tx.q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE refresh_sha256 = ?`, sum[:]))
}

// SessionByPrevRefresh returns the session whose previous (already rotated) refresh token
// hashes to sum, or ErrNoSession. Finding one means the old token was presented again.
func (tx *UsersReadTx) SessionByPrevRefresh(ctx context.Context, sum [32]byte) (model.Session, error) {
	return scanSession(tx.q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE prev_refresh_sha256 = ?`,
		sum[:]))
}

// UserSessions returns a user's sessions that are active at now, newest first.
func (tx *UsersReadTx) UserSessions(ctx context.Context, userID int64, now time.Time) (out []model.Session, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE user_id = ? AND revoked_at = 0 AND expires_at > ? ORDER BY id DESC`, userID, now.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RotateSession replaces a session's refresh token (old becomes the previous one) and extends
// it, only if old is still its current token. It reports whether it did.
func (tx *UsersTx) RotateSession(ctx context.Context, id int64, old, next [32]byte, at, expires time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE sessions SET prev_refresh_sha256 = refresh_sha256, refresh_sha256 = ?,
		last_used_at = ?, expires_at = ? WHERE id = ? AND refresh_sha256 = ? AND revoked_at = 0`,
		next[:], zeroUnix(at), zeroUnix(expires), id, old[:])
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RevokeSession revokes a session (a revoked one keeps its first time). It is ErrNoSession
// when there is none.
func (tx *UsersTx) RevokeSession(ctx context.Context, id int64, at time.Time) error {
	return execOne(ctx, tx.q, ErrNoSession, `UPDATE sessions SET revoked_at = CASE revoked_at WHEN 0 THEN ?
		ELSE revoked_at END WHERE id = ?`, zeroUnix(at), id)
}

// RevokeUserSession revokes a session only if it belongs to userID (`WHERE id=? AND
// user_id=?`): another user's session is ErrNoSession, exactly like a missing one.
func (tx *UsersTx) RevokeUserSession(ctx context.Context, userID, id int64, at time.Time) error {
	return execOne(ctx, tx.q, ErrNoSession, `UPDATE sessions SET revoked_at = CASE revoked_at WHEN 0 THEN ?
		ELSE revoked_at END WHERE id = ? AND user_id = ?`, zeroUnix(at), id, userID)
}

// RevokeUserSessions revokes every unrevoked session of a user except keepID (0 keeps none).
func (tx *UsersTx) RevokeUserSessions(ctx context.Context, userID, keepID int64, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND id != ? AND revoked_at = 0`,
		zeroUnix(at), userID, keepID)
	return err
}

// DeleteExpiredSessions deletes sessions that expired at or before now (revoked or not: an
// expired refresh token is refused either way).
func (tx *UsersTx) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	return err
}

// ---- signing keys ----

// SigningKeys returns every stored signing key, oldest first.
func (tx *UsersReadTx) SigningKeys(ctx context.Context) (out []model.SigningKey, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT kid, private_key_enc, created_at, retired_at FROM signing_keys
		ORDER BY created_at, kid`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var k model.SigningKey
		var created, retired int64
		if err := rows.Scan(&k.ID, &k.SeedEnc, &created, &retired); err != nil {
			return nil, err
		}
		k.CreatedAt, k.RetiredAt = unixTime(created), unixTime(retired)
		out = append(out, k)
	}
	return out, rows.Err()
}

// AddSigningKey stores a new active signing key.
func (tx *UsersTx) AddSigningKey(ctx context.Context, k model.SigningKey) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO signing_keys (kid, private_key_enc, created_at) VALUES (?,?,?)`,
		k.ID, k.SeedEnc, zeroUnix(k.CreatedAt))
	return err
}

// RetireSigningKeys marks every active signing key retired at at.
func (tx *UsersTx) RetireSigningKeys(ctx context.Context, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `UPDATE signing_keys SET retired_at = ? WHERE retired_at = 0`, zeroUnix(at))
	return err
}

// DeleteSigningKeysRetiredBy deletes keys retired at or before t (their tokens have expired).
func (tx *UsersTx) DeleteSigningKeysRetiredBy(ctx context.Context, t time.Time) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM signing_keys WHERE retired_at != 0 AND retired_at <= ?`, t.Unix())
	return err
}
