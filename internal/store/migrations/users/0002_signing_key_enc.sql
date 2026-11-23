-- users.db v2: JWT signing seeds are encrypted under a master key from keys.db
-- (docs/part3-key-rotation.md stage 2). The old rows held seeds in the clear and are dropped;
-- serve creates a new key at start (no users yet, so no tokens to keep).

DROP TABLE signing_keys;

-- Ed25519 keys that sign access tokens. One active key (retired_at = 0); retired keys stay
-- until their tokens have expired.
CREATE TABLE signing_keys (
    kid             TEXT PRIMARY KEY,
    private_key_enc BLOB NOT NULL,  -- the 32-byte seed, keyring envelope with AAD "signing-key:<kid>"
    created_at      INTEGER NOT NULL,
    retired_at      INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
