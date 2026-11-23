package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// ErrNoUser is a user ID or username with no account.
var ErrNoUser = errors.New("store: no such user")

const userCols = `id, username, password_phc, totp_secret_enc, last_totp_step, created_at, last_login_at,
	password_changed_at, disabled_at, failed_attempts, next_attempt_at`

func scanUser(row interface{ Scan(...any) error }) (model.User, error) {
	var u model.User
	var created, login, changed, disabled, next int64
	err := row.Scan(&u.ID, &u.Username, &u.PasswordPHC, &u.TOTPSecretEnc, &u.LastTOTPStep, &created, &login,
		&changed, &disabled, &u.FailedAttempts, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNoUser
	}
	u.CreatedAt, u.LastLoginAt, u.PasswordChangedAt = unixTime(created), unixTime(login), unixTime(changed)
	u.DisabledAt, u.NextAttemptAt = unixTime(disabled), unixTime(next)
	return u, err
}

// UserByID returns an account, or ErrNoUser.
func (tx *UsersReadTx) UserByID(ctx context.Context, id int64) (model.User, error) {
	return scanUser(tx.q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// UserByName returns the account with a (normalized) username, or ErrNoUser.
func (tx *UsersReadTx) UserByName(ctx context.Context, username string) (model.User, error) {
	return scanUser(tx.q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, username))
}

// UsernameTaken reports whether an account has the (normalized) username.
func (tx *UsersReadTx) UsernameTaken(ctx context.Context, username string) (bool, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username = ?`, username).Scan(&n)
	return n > 0, err
}

// ListUsers returns every account by ID.
func (tx *UsersReadTx) ListUsers(ctx context.Context) (out []model.User, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser inserts an account (ID, the attempt counters and LastLoginAt are ignored) and
// returns its ID. The caller checks UsernameTaken first in the same transaction.
func (tx *UsersTx) CreateUser(ctx context.Context, u model.User) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `INSERT INTO users (username, password_phc, totp_secret_enc, last_totp_step,
		created_at, password_changed_at) VALUES (?,?,?,?,?,?)`,
		u.Username, u.PasswordPHC, u.TOTPSecretEnc, u.LastTOTPStep, zeroUnix(u.CreatedAt), zeroUnix(u.PasswordChangedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetUserTOTP replaces an account's sealed TOTP secret and its last accepted step.
func (tx *UsersTx) SetUserTOTP(ctx context.Context, id int64, enc []byte, lastStep int64) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET totp_secret_enc = ?, last_totp_step = ? WHERE id = ?`,
		enc, lastStep, id)
}

// SetUserTOTPStep records the last accepted TOTP step (only ever forward).
func (tx *UsersTx) SetUserTOTPStep(ctx context.Context, id, step int64) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET last_totp_step = MAX(last_totp_step, ?) WHERE id = ?`,
		step, id)
}

// SetUserPassword replaces an account's password hash and records when it changed.
func (tx *UsersTx) SetUserPassword(ctx context.Context, id int64, phc string, at time.Time) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET password_phc = ?, password_changed_at = ? WHERE id = ?`,
		phc, zeroUnix(at), id)
}

// RehashUserPassword stores an upgraded hash of the same password, only if the stored hash is
// still old (a concurrent password change wins). It reports whether it did.
func (tx *UsersTx) RehashUserPassword(ctx context.Context, id int64, old, phc string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE users SET password_phc = ? WHERE id = ? AND password_phc = ?`, phc, id, old)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetUserAttempts records an account's consecutive failures and its backoff.
func (tx *UsersTx) SetUserAttempts(ctx context.Context, id int64, failed int, next time.Time) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET failed_attempts = ?, next_attempt_at = ? WHERE id = ?`,
		failed, zeroUnix(next), id)
}

// RecordUserLogin records a full login: the time, and the failures and backoff cleared.
func (tx *UsersTx) RecordUserLogin(ctx context.Context, id int64, at time.Time) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET last_login_at = ?, failed_attempts = 0, next_attempt_at = 0
		WHERE id = ?`, zeroUnix(at), id)
}

// DisableUser marks an account disabled at at (a disabled one keeps its first time).
func (tx *UsersTx) DisableUser(ctx context.Context, id int64, at time.Time) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET disabled_at = CASE disabled_at WHEN 0 THEN ? ELSE disabled_at END
		WHERE id = ?`, zeroUnix(at), id)
}

// EnableUser clears an account's disabled mark, failures and backoff.
func (tx *UsersTx) EnableUser(ctx context.Context, id int64) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET disabled_at = 0, failed_attempts = 0, next_attempt_at = 0
		WHERE id = ?`, id)
}

// DeleteUser deletes an account with its recovery codes, sessions, challenges and reset invites.
func (tx *UsersTx) DeleteUser(ctx context.Context, id int64) error {
	return execOne(ctx, tx.q, ErrNoUser, `DELETE FROM users WHERE id = ?`, id)
}

// ReplaceRecoveryCodes replaces every recovery code of an account with encs (sealed, unused).
func (tx *UsersTx) ReplaceRecoveryCodes(ctx context.Context, userID int64, encs [][]byte) error {
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, enc := range encs {
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO recovery_codes (user_id, code_enc) VALUES (?,?)`,
			userID, enc); err != nil {
			return err
		}
	}
	return nil
}

// UnusedRecoveryCodes returns an account's unused recovery codes, by ID.
func (tx *UsersReadTx) UnusedRecoveryCodes(ctx context.Context, userID int64) (out []model.RecoveryCode, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT id, code_enc FROM recovery_codes
		WHERE user_id = ? AND used_at = 0 ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var c model.RecoveryCode
		if err := rows.Scan(&c.ID, &c.CodeEnc); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UseRecoveryCode marks the unused recovery code id of an account used. It reports whether
// there was one.
func (tx *UsersTx) UseRecoveryCode(ctx context.Context, userID, id int64, at time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE recovery_codes SET used_at = ?
		WHERE id = ? AND user_id = ? AND used_at = 0`, zeroUnix(at), id, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecoveryCodesLeft counts an account's unused recovery codes.
func (tx *UsersReadTx) RecoveryCodesLeft(ctx context.Context, userID int64) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at = 0`,
		userID).Scan(&n)
	return n, err
}

// execOne runs a statement that must change exactly one row; none is notFound.
func execOne(ctx context.Context, q querier, notFound error, query string, args ...any) error {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	return nil
}
