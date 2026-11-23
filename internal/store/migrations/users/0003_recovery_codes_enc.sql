-- users.db v3: recovery codes are stored as the SHA-256 of the normalized code, encrypted under
-- a master key from keys.db (docs/part3-key-rotation.md stage 5), so rotation can re-encrypt
-- them without the codes. The old rows were HMACs under a master-key subkey and are dropped
-- (no users yet, so no codes to keep).

DROP TABLE recovery_codes;

-- Random nonces make the stored values unequal for equal codes, so a code is found by
-- decrypting each of the user's unused codes, not by lookup.
CREATE TABLE recovery_codes (
    id       INTEGER PRIMARY KEY,
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_enc BLOB NOT NULL,  -- SHA-256 of the normalized code, keyring envelope with AAD "recovery:<user id>"
    used_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX recovery_codes_user ON recovery_codes(user_id, used_at);
