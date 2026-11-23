-- keys.db v1: the master keys (docs/part3-key-rotation.md). A separate database from users.db,
-- so a copy of users.db alone decrypts nothing. Only `serve` opens it. Times are unix seconds.

CREATE TABLE master_keys (
    id         INTEGER PRIMARY KEY AUTOINCREMENT, -- the key ID stored beside every ciphertext; never reused
    key        BLOB NOT NULL CHECK (length(key) = 32),
    created_at INTEGER NOT NULL,
    retired_at INTEGER NOT NULL DEFAULT 0          -- 0 = active; exactly one active key
);

-- At most one active key.
CREATE UNIQUE INDEX master_keys_one_active ON master_keys (retired_at) WHERE retired_at = 0;
