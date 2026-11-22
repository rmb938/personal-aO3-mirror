package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

var (
	// ErrNoInvite is an invite ID or token with no invite.
	ErrNoInvite = errors.New("store: no such invite")
	// ErrNoEnrollment is an enrollment token with no pending signup or reset.
	ErrNoEnrollment = errors.New("store: no such enrollment")
)

const inviteCols = `id, kind, COALESCE(target_user_id, 0), reset_scope, created_at, expires_at, used_at,
	COALESCE(used_by, 0), revoked_at`

func scanInvite(row interface{ Scan(...any) error }) (model.Invite, error) {
	var i model.Invite
	var created, expires, used, revoked int64
	err := row.Scan(&i.ID, &i.Kind, &i.TargetUserID, &i.Scope, &created, &expires, &used, &i.UsedBy, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNoInvite
	}
	i.CreatedAt, i.ExpiresAt, i.UsedAt, i.RevokedAt = unixTime(created), unixTime(expires), unixTime(used), unixTime(revoked)
	return i, err
}

// CreateInvite inserts an invite with its token's hash and returns its ID (i.ID and the used
// and revoked fields are ignored).
func (tx *UsersTx) CreateInvite(ctx context.Context, i model.Invite, tokenSum [32]byte) (int64, error) {
	var target any
	if i.TargetUserID != 0 {
		target = i.TargetUserID
	}
	res, err := tx.q.ExecContext(ctx, `INSERT INTO invites (token_sha256, kind, target_user_id, reset_scope,
		created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		tokenSum[:], string(i.Kind), target, string(i.Scope), zeroUnix(i.CreatedAt), zeroUnix(i.ExpiresAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InviteByToken returns the invite whose token hashes to sum, or ErrNoInvite.
func (tx *UsersReadTx) InviteByToken(ctx context.Context, sum [32]byte) (model.Invite, error) {
	return scanInvite(tx.q.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites WHERE token_sha256 = ?`, sum[:]))
}

// InviteByID returns an invite, or ErrNoInvite.
func (tx *UsersReadTx) InviteByID(ctx context.Context, id int64) (model.Invite, error) {
	return scanInvite(tx.q.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites WHERE id = ?`, id))
}

// ListInvites returns every invite, newest first.
func (tx *UsersReadTx) ListInvites(ctx context.Context) (out []model.Invite, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT `+inviteCols+` FROM invites ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// UseInvite marks an unused, unrevoked invite used by an account. It reports whether it did.
func (tx *UsersTx) UseInvite(ctx context.Context, id, by int64, at time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE invites SET used_at = ?, used_by = ?
		WHERE id = ? AND used_at = 0 AND revoked_at = 0`, zeroUnix(at), by, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RevokeInvite marks an invite revoked (a used or revoked one is left as it is) and deletes its
// pending enrollment. A missing invite is ErrNoInvite.
func (tx *UsersTx) RevokeInvite(ctx context.Context, id int64, at time.Time) error {
	if err := execOne(ctx, tx.q, ErrNoInvite, `UPDATE invites SET revoked_at = CASE
		WHEN used_at = 0 AND revoked_at = 0 THEN ? ELSE revoked_at END WHERE id = ?`, zeroUnix(at), id); err != nil {
		return err
	}
	return tx.DeleteEnrollment(ctx, id)
}

// PutEnrollment stores an invite's pending signup or reset under its token's hash, replacing
// any earlier one for the same invite.
func (tx *UsersTx) PutEnrollment(ctx context.Context, e model.Enrollment, tokenSum [32]byte) error {
	if err := tx.DeleteEnrollment(ctx, e.InviteID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `INSERT INTO enrollments (token_sha256, invite_id, username, password_phc,
		totp_secret_enc, expires_at) VALUES (?,?,?,?,?,?)`,
		tokenSum[:], e.InviteID, e.Username, e.PasswordPHC, e.TOTPSecretEnc, zeroUnix(e.ExpiresAt))
	return err
}

// EnrollmentByToken returns the enrollment whose token hashes to sum, or ErrNoEnrollment.
func (tx *UsersReadTx) EnrollmentByToken(ctx context.Context, sum [32]byte) (model.Enrollment, error) {
	var e model.Enrollment
	var expires int64
	err := tx.q.QueryRowContext(ctx, `SELECT invite_id, username, password_phc, totp_secret_enc, expires_at
		FROM enrollments WHERE token_sha256 = ?`, sum[:]).Scan(&e.InviteID, &e.Username, &e.PasswordPHC,
		&e.TOTPSecretEnc, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNoEnrollment
	}
	e.ExpiresAt = unixTime(expires)
	return e, err
}

// DeleteEnrollment deletes an invite's pending enrollment, if any.
func (tx *UsersTx) DeleteEnrollment(ctx context.Context, inviteID int64) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM enrollments WHERE invite_id = ?`, inviteID)
	return err
}

// DeleteExpiredEnrollments deletes enrollments that expired at or before now.
func (tx *UsersTx) DeleteExpiredEnrollments(ctx context.Context, now time.Time) error {
	_, err := tx.q.ExecContext(ctx, `DELETE FROM enrollments WHERE expires_at <= ?`, now.Unix())
	return err
}
