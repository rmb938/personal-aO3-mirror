package store

import (
	"context"
	"errors"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Master key rotation (docs/part3-key-rotation.md "Rotation") re-encrypts every value in
// users.db that is encrypted under a master key. The tables are small (a few accounts), so it
// reads whole tables and the caller skips values already under the active key; which key a
// value names is the keyring's format, not the store's. The reads for users and signing keys
// are ListUsers and SigningKeys.

// SetUserSecrets replaces an account's password hash and sealed TOTP secret with re-encrypted
// forms of the same values (password_changed_at and last_totp_step stay).
func (tx *UsersTx) SetUserSecrets(ctx context.Context, id int64, phc string, totpEnc []byte) error {
	return execOne(ctx, tx.q, ErrNoUser, `UPDATE users SET password_phc = ?, totp_secret_enc = ? WHERE id = ?`,
		phc, totpEnc, id)
}

// Enrollments returns every pending enrollment by invite ID, expired ones included.
func (tx *UsersReadTx) Enrollments(ctx context.Context) (out []model.Enrollment, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT invite_id, username, password_phc, totp_secret_enc, expires_at
		FROM enrollments ORDER BY invite_id`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var e model.Enrollment
		var expires int64
		if err := rows.Scan(&e.InviteID, &e.Username, &e.PasswordPHC, &e.TOTPSecretEnc, &expires); err != nil {
			return nil, err
		}
		e.ExpiresAt = unixTime(expires)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetEnrollmentSecrets replaces an enrollment's password hash and sealed TOTP secret with
// re-encrypted forms of the same values ("" and nil keep meaning "keep the current one").
func (tx *UsersTx) SetEnrollmentSecrets(ctx context.Context, inviteID int64, phc string, totpEnc []byte) error {
	return execOne(ctx, tx.q, ErrNoEnrollment, `UPDATE enrollments SET password_phc = ?, totp_secret_enc = ?
		WHERE invite_id = ?`, phc, totpEnc, inviteID)
}

// RecoveryCodes returns every account's recovery codes by ID, used ones included, with UserID
// set.
func (tx *UsersReadTx) RecoveryCodes(ctx context.Context) (out []model.RecoveryCode, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT id, user_id, code_enc FROM recovery_codes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var c model.RecoveryCode
		if err := rows.Scan(&c.ID, &c.UserID, &c.CodeEnc); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrNoRecoveryCode is a recovery code ID with no code.
var ErrNoRecoveryCode = errors.New("store: no such recovery code")

// SetRecoveryCodeEnc replaces a recovery code's sealed digest with a re-encrypted form of it.
func (tx *UsersTx) SetRecoveryCodeEnc(ctx context.Context, id int64, enc []byte) error {
	return execOne(ctx, tx.q, ErrNoRecoveryCode, `UPDATE recovery_codes SET code_enc = ? WHERE id = ?`, enc, id)
}

// ErrNoSigningKey is a kid with no stored signing key.
var ErrNoSigningKey = errors.New("store: no such signing key")

// SetSigningKeySeed replaces a signing key's encrypted seed with a re-encrypted form of it.
func (tx *UsersTx) SetSigningKeySeed(ctx context.Context, kid string, enc []byte) error {
	return execOne(ctx, tx.q, ErrNoSigningKey, `UPDATE signing_keys SET private_key_enc = ? WHERE kid = ?`, enc, kid)
}
