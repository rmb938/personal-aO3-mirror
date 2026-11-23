-- users.db v1: the API's accounts (docs/part2-api.md stage 16). A separate database from
-- meta.db and content.db: only `serve` opens it. Times are unix seconds, 0 = never.
-- Secrets are never stored in the clear: passwords as Argon2id PHC strings, TOTP secrets
-- sealed under a master key from keys.db (not in this file; docs/part3-key-rotation.md),
-- opaque tokens as SHA-256, recovery codes as HMAC-SHA256.

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT, -- never reused: it is the JWT sub
    username            TEXT NOT NULL UNIQUE,              -- lower case, [a-z0-9_-]{3,32}
    password_phc        TEXT NOT NULL,
    totp_secret_enc     BLOB NOT NULL,                     -- AAD = user id
    last_totp_step      INTEGER NOT NULL DEFAULT 0,        -- replay guard; 0 = never used
    created_at          INTEGER NOT NULL,
    last_login_at       INTEGER NOT NULL DEFAULT 0,
    password_changed_at INTEGER NOT NULL,
    disabled_at         INTEGER NOT NULL DEFAULT 0,
    failed_attempts     INTEGER NOT NULL DEFAULT 0,        -- consecutive; reset on a full login
    next_attempt_at     INTEGER NOT NULL DEFAULT 0         -- backoff: no attempt before this
);

CREATE TABLE recovery_codes (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hmac BLOB NOT NULL,
    used_at   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, code_hmac)
) WITHOUT ROWID;

CREATE TABLE invites (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    token_sha256   BLOB NOT NULL UNIQUE,
    kind           TEXT NOT NULL CHECK (kind IN ('signup','reset')),
    target_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE, -- reset only
    reset_scope    TEXT NOT NULL DEFAULT '' CHECK (reset_scope IN ('','password','totp','both')),
    created_at     INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL,
    used_at        INTEGER NOT NULL DEFAULT 0,
    used_by        INTEGER REFERENCES users(id) ON DELETE SET NULL,
    revoked_at     INTEGER NOT NULL DEFAULT 0,
    CHECK ((kind = 'signup') = (target_user_id IS NULL AND reset_scope = ''))
);

-- A pending signup or reset between its two steps. One per invite: a new step 1 replaces it.
CREATE TABLE enrollments (
    token_sha256    BLOB PRIMARY KEY,
    invite_id       INTEGER NOT NULL UNIQUE REFERENCES invites(id) ON DELETE CASCADE,
    username        TEXT NOT NULL DEFAULT '',  -- signup only
    password_phc    TEXT NOT NULL DEFAULT '',  -- '' = keep the current password (reset)
    totp_secret_enc BLOB,                      -- NULL = keep the current TOTP (reset); AAD = invite id
    expires_at      INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX enrollments_expires ON enrollments(expires_at);

-- A login between its password and second-factor steps.
CREATE TABLE login_challenges (
    token_sha256 BLOB PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   INTEGER NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
CREATE INDEX login_challenges_expires ON login_challenges(expires_at);

CREATE TABLE sessions (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT, -- never reused: it is the JWT sid
    user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_sha256      BLOB NOT NULL UNIQUE,
    prev_refresh_sha256 BLOB,                              -- presenting it again revokes (reuse)
    created_at          INTEGER NOT NULL,
    last_used_at        INTEGER NOT NULL,                  -- updated on refresh, not per request
    expires_at          INTEGER NOT NULL,
    revoked_at          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_prev_refresh ON sessions(prev_refresh_sha256) WHERE prev_refresh_sha256 IS NOT NULL;
CREATE INDEX sessions_expires ON sessions(expires_at);

-- Ed25519 keys that sign access tokens. One active key (retired_at = 0); retired keys stay
-- until their tokens have expired.
CREATE TABLE signing_keys (
    kid         TEXT PRIMARY KEY,
    private_key BLOB NOT NULL,  -- the 32-byte seed
    created_at  INTEGER NOT NULL,
    retired_at  INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
