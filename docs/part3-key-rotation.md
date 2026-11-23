# Part 3 — Master key rotation plan (keys.db)

> How to use this file: each stage below is one PR, followed by `/clear`. A new session reads
> CLAUDE.md, then this file's "Context", "Design" and its own stage section, implements it, ticks
> the stage's box, runs `make check`, then **stops and waits**: the user reviews the uncommitted
> changes and says when to commit and open the PR. Never commit, push or open a PR unasked. If a
> stage changes a decision, update this file in the same change. API work (routes, problems,
> request types, OpenAPI, `make generate`, the all-routes leak test) follows `docs/part2-api.md`
> "Conventions"; read only the parts a stage needs.

## Context

Part 2 stage 16 added accounts in `data/api/users.db`, protected by one 32-byte master key in
`data/api/auth.key` (`internal/auth/secrets.go`). HKDF derives four subkeys from it: TOTP
encryption, recovery-code HMAC, the unknown-user backoff's name HMAC, and (17f) the password
pepper. Two gaps:

1. **auth.key never rotates.** It has no ID, and two of its uses can't be re-keyed without the
   user: the pepper is applied before Argon2id (re-keying needs the password) and recovery codes
   are a bare HMAC (re-keying needs the codes).
2. **The JWT signing seeds are stored in the clear** in `signing_keys.private_key`. A copy of
   users.db alone lets anyone mint access tokens for any active session until the key rotates
   out (up to `signing_key_rotation` + grace, 30 days by default). Everything else in users.db
   needs auth.key.

Part 3 replaces auth.key with `keys.db`, a store of versioned master keys. Every secret in
users.db is encrypted or wrapped under one of them and records its key ID, and `serve` rotates
the master key on a schedule, re-encrypting everything without any user present.

### Decisions made with the user (2026-11-23)

| Topic | Decision |
|---|---|
| Goal | Rotate the master key routinely, like the JWT signing key, with zero setup: "run the binary and go". |
| Key store | A separate SQLite database `data/api/keys.db`, not a file and not users.db (a copy of users.db must stay useless on its own). |
| Passwords | **Encrypt the hash**: XChaCha20-Poly1305 over the Argon2id output, replacing 17f's HMAC pepper before Argon2id, so re-keying needs no password. |
| Recovery codes | **Encrypt the hash**, so they re-key the same way. The inner step is plain SHA-256 (the codes have 120 bits): an inner HMAC keyed from the rotating master key would stop matching once that key is deleted. |
| Crypto library | XChaCha20-Poly1305 from `golang.org/x/crypto/chacha20poly1305` (already a dependency), not Google Tink: the rotation logic is ours either way, and Tink would add protobuf and a large API to replace a few lines. The ciphertext layout borrows Tink's (version byte, fixed 4-byte key ID). |
| Cipher | XChaCha20-Poly1305 rather than AES-256-GCM, chosen by the user: its 192-bit random nonces never realistically collide, where GCM's 96-bit ones are limited to 2³² encryptions per key, and it is constant-time without hardware AES. It is not NIST-approved; no doc claims approved encryption or FIPS (Argon2id isn't approved either), and comments must not claim it. |
| JWT seeds | Encrypted under the master key. |
| Old keys | Deleted as soon as no row references them, not after a fixed time. |
| Backups | **Option (a)**: an `ao3mirror backup` command snapshots users.db and keys.db together under the rotation lock. Not (b) (keep retired keys N days), because it leaves old keys in the live keys.db, where they decrypt older users.db copies. |
| TPM | Rejected for now: it needs `tss` group membership or a systemd-creds setup, which breaks zero setup. Hardware-bound keys should be a deliberate user choice, never auto-detected. |
| Migration | No users exist yet (as in 17e/17f), so format changes need no data migration. |

## Design

### Threat model

- **Protects:** any copy of users.db without the matching keys.db (a stray backup, a synced
  folder, a copied file) is useless: no token forging, no offline password guessing, no TOTP
  secrets, no recovery codes. After a rotation, an *older* users.db copy is useless even
  together with the *current* keys.db.
- **Doesn't protect:** an attacker with ongoing access as `serve`'s user, who reads both
  databases. Rotation limits how long what they took stays useful, but they still have the
  underlying Argon2id hashes and TOTP secrets, which rotation re-wraps but never changes.
- **Accepted:** filesystem and SSD remnants of deleted pages or journal files. Backups hold the
  keys they were taken with, so backup retention is the user's key retention (documented).

### keys.db

```sql
CREATE TABLE master_keys (
    id         INTEGER PRIMARY KEY AUTOINCREMENT, -- the key ID stored beside every ciphertext; never reused
    key        BLOB NOT NULL,                      -- 32 random bytes
    created_at INTEGER NOT NULL,
    retired_at INTEGER NOT NULL DEFAULT 0          -- 0 = active; exactly one active key
);
```

- It lives in the admin socket's directory next to users.db, 0600 in a 0700 directory, created
  and checked like users.db (`createPrivate`, `privateDir`). The guard already covers it (`*.db`
  under `data/`).
- It opens with `journal_mode=DELETE` (not WAL; it gets a handful of writes), `synchronous=FULL`
  (NORMAL is only safe in WAL mode) and `secure_delete=ON`, so a deleted key doesn't stay
  readable in a free page or a WAL frame. `openDB` takes a `dbOptions` (page size, rollback
  journal, extra pragmas) rather than hard-coding WAL. Only keys.db itself is pre-created 0600;
  SQLite gives the short-lived `-journal` the database's mode.
- A partial unique index (`WHERE retired_at = 0`) enforces at most one active key, and a CHECK
  enforces 32-byte keys.
- `serve` creates it with one active key on first start (`controllers.LoadKeyring`, logging
  `master_key_created` with the key ID). Only `serve` opens it.
- Store code: `internal/store/keys.go` (`OpenKeys`, `KeysReadTx`/`KeysTx`, `InTx`/`InReadTx`,
  `ReadKeysValue`/`WriteKeysValue`, its own migrations directory), the same shape as users.db.

### The keyring (`internal/auth`)

`Secrets` becomes `Keyring`: the master keys by ID, the active ID, and per key the HKDF-SHA256
subkeys (`"ao3mirror totp-enc"`, `"ao3mirror password-enc"`, `"ao3mirror recovery-enc"`,
`"ao3mirror signing-key-enc"`). Only the outer encryption is keyed: no inner hash or MAC may
use a key derived from a master key, because rotation re-wraps the outer layer and then deletes
the old key. It is immutable: a rotation builds a
new one and swaps it atomically, like the JWT `keyRing`.

- **Encrypt** always uses the active key and writes its ID into the ciphertext. **Decrypt**
  reads the ID and uses that key, or fails with a "key not held" error.
- Every ciphertext is XChaCha20-Poly1305 (`chacha20poly1305.NewX`) with a random 192-bit nonce
  and AAD binding it to its row:
  `version(1) | key_id(4, big-endian) | nonce(24) | ciphertext+tag(16)` (Tink's layout). The
  version and key ID bytes are also part of the AAD, so they can't be altered undetected. The fixed
  width lets SQL select rows under a key with `substr(col, 2, 4)`; master key IDs are bounded to
  `uint32`.
  | Value | Column | AAD |
  |---|---|---|
  | TOTP secret | `users.totp_secret_enc`, `enrollments.totp_secret_enc` | `user:<id>` / `enrollment:<invite id>` (as now) |
  | Password hash | `users.password_phc`, `enrollments.password_phc` | `user:<id>` / `enrollment:<invite id>`, plus the PHC prefix (params and salt) |
  | Recovery code | `recovery_codes.code_enc` | `recovery:<user id>` |
  | JWT signing seed | `signing_keys.private_key_enc` | `signing-key:<kid>` |
- **Encrypt inside the write transaction that stores the value**, using the keyring loaded at
  that moment. Argon2id (slow) stays outside the transaction, as now. Only the cheap encryption
  step moves inside. That keeps a value encrypted under a key being retired from landing after the
  re-encryption pass, apart from the straggler case the rotation handles (stage 5).
- The unknown-user backoff's `NameMAC` keys in-memory state only, so it moves to a random
  per-process key and leaves the keyring. Nothing about it needs to survive a restart or rotation.

### Rotation (stage 5)

The two databases can't commit atomically together (SQLite only spans `ATTACH`ed databases
without WAL), so rotation is ordered so that every crash point leaves every row's key present:

1. **keys.db transaction:** retire the active key, insert a new active key.
2. **Memory:** swap in the new keyring (both keys held). New writes use the new key.
3. **users.db transaction:** re-encrypt every value whose key ID is not the active one. Pure
   decrypt/encrypt work in memory, no Argon2id.
4. **users.db read, then keys.db transaction:** delete each retired key that no row references.
   Swap in a keyring without it. If a straggler still references it (a write that read the old
   keyring before step 2), repeat step 3 once. If one still remains, leave the key and log; the
   next run deletes it.

| Crash after | State | Next start |
|---|---|---|
| 1 | Two keys; rows use the old one | Finishes 3 and 4 |
| 3 | Rows use the new key; the old key is stored | Finishes 4 |
| 4 | Done | Nothing |

`serve` runs "finish rotation" at startup (re-encrypt anything not under the active key, then
delete unreferenced retired keys), before it listens. It also refuses to start when users.db
references a key ID that keys.db doesn't hold (for example a mismatched restore). Every
rotation, backup and finish step holds one in-process rotation mutex. Only `serve` writes either
database.

### Backup (stage 6)

`ao3mirror backup <dir>` (an admin socket client) asks `serve` to snapshot both databases while
holding the rotation mutex. `serve` creates `<dir>` (0700; it must not exist), pre-creates
`users.db` and `keys.db` there 0600, and runs `VACUUM INTO` for each (into an empty file). The
copies are consistent with each other and carry no free pages. Restore is documented: stop
`serve`, copy both files into `data/api/`, start. The startup check catches a mismatched pair.
meta.db and content.db aren't part of it (they hold no key-dependent data).

## Stages

- [x] **1. keys.db replaces auth.key.**
  - `internal/store/keys.go` and its migrations, `openDB` options for journal mode and pragmas,
    and permission checks as for users.db.
  - `auth.Keyring` replaces `Secrets`, loaded from keys.db (one key, the first created on first
    start), with TOTP secrets moved to the keyring envelope (version 2: XChaCha20-Poly1305 with
    the key ID; AES-GCM is gone) and the PHC `k=` meaning the
    master key ID (the pepper-before-Argon2id design stays until stage 3).
  - `NameMAC` moves to a random per-process key (`nameBackoff.key` in the controller).
  - `serve`'s `openAuth` opens keys.db, and auth.key is no longer read (no import or warning:
    no users yet; the user deletes the old file).
  - Docs: `KeyFileName` and comments that cite auth.key, the backup note in `Secrets`/the
    users.db migration comment, and `ao3mirror.example.yaml` if it mentions auth.key.
  - Tests: keys.db store tests (create, permissions, `secure_delete` and journal mode applied),
    keyring encrypt/decrypt round trips, a wrong key ID, a tampered key ID byte, wrong AAD.
- [ ] **2. Encrypt the JWT signing seeds.**
  - users.db migration: `signing_keys.private_key` becomes `private_key_enc`. Existing rows are
    deleted (serve creates a new key; no users, so no tokens to keep).
  - `loadKeys` / `rotateSigningKey` encrypt inside their transaction and decrypt at load.
  - Tests: a users.db row holds no seed bytes in the clear (the stored blob differs from the
    seed and doesn't decrypt under another keyring), and load round trips.
- [ ] **3. Password hashes in the encrypted-hash format.**
  - PHC string: `$argon2id$v=19$m=…,t=…,p=…,k=<key id>$<salt>$<base64(nonce|ct)>`, the
    ciphertext being the keyring envelope (as above) of the raw Argon2id output with the AAD above. The HMAC pepper is
    removed (Argon2id hashes the NFC password directly).
  - `Hasher.Hash` returns the unwrapped hash. The caller wraps it with the keyring inside the
    write transaction. `Verify` takes the stored string, unwraps it (a key not held or a
    decrypt failure is a server error, not "wrong password"), then compares in constant time.
    The dummy hash for unknown users does the same unwrap plus Argon2id, so timing matches.
    `NeedsRehash` compares Argon2 parameters only (the key ID is rotation's job, not login's).
  - Enrollments wrap with the enrollment AAD, and confirming re-wraps for the user, as TOTP does.
  - Update the 63B-4 citations: the keyed step is now encryption after hashing (§3.1.1.2 allows
    keyed hashing or encryption; re-check the exact wording when writing the comment).
  - Tests: `TestHasherPepper` becomes the wrap tests (another keyring doesn't verify, a copied
    row fails under another user's AAD, an unknown key ID), timing tests kept, `FuzzPHC` updated.
- [ ] **4. Recovery codes as an encrypted hash.**
  - users.db migration: `recovery_codes` becomes `(id INTEGER PRIMARY KEY, user_id, code_enc,
    used_at)`. The value is the keyring envelope of the SHA-256 of the normalized code (AAD
    `recovery:<user id>`). The `recovery-hmac` subkey and `RecoveryCodeMAC` go away; update the
    §3.1.2.2 comment (an approved hash of a 120-bit code, then encrypted).
  - Random nonces make lookup by equality impossible, so `UseRecoveryCode` loads the user's
    unused codes (at most the issued count), decrypts each and compares in constant time,
    checking every code, not stopping at the first match.
  - Tests: use-once, wrong code, another user's code, codes under another keyring.
- [ ] **5. Rotation.**
  - Config: `auth.master_key_rotation: 720h` (24h..8760h, like `signing_key_rotation`).
  - Controller: `RotateMasterKey` (admin) and `RotateMasterKeyIfDue` (scheduled), the startup
    "finish rotation" and key-ID consistency check, all under one mutex, with the four steps
    above. The store gets the queries for "rows not under key N" per table and "key IDs in use".
  - `serve` runs the due check with the signing-key check (startup, then hourly).
  - Route `POST /api/v1/admin/master-keys/rotate` (204) and CLI `ao3mirror auth
    rotate-master-key`. Per Part 2 conventions: route constant, OpenAPI, `make generate`, an
    all-routes leak test sample.
  - Log events: `master_key_rotated` (reason admin/scheduled, rows re-encrypted per table,
    key IDs), `master_key_deleted`, `master_key_straggler`. No key material.
  - Tests (clock-driven, temp stores): due/not due; a crash injected after each step, then a
    restart that finishes it; a straggler written between steps 2 and 3; two triggers at once
    rotate once; a users.db referencing a missing key refuses to start; every value type
    decrypts after rotation and the old key is gone from keys.db.
- [ ] **6. Backup.**
  - Route `POST /api/v1/admin/backup` with a `request` body `{dir}` (absolute, cleaned, bounded
    length), 201 `{dir}` (or 204), a problem for "target exists", and CLI `ao3mirror backup
    <dir>` (resolves a relative path to an absolute one client-side).
  - Controller: under the rotation mutex, create the directory 0700, pre-create both files
    0600, `VACUUM INTO` each.
  - Docs: a backup and restore section (what is in a backup, that it is as sensitive as the live
    data, that retention is key retention, and how to restore).
  - Tests: a backup restores (open both copies, every value decrypts), the copies are 0600 in a
    0700 dir, an existing target is refused, a backup during a rotation waits for it.

## Verification

- `make check` passes after every stage.
- After stage 5, a manual check the user runs (the guard keeps Claude out of `data/`):
  `ao3mirror auth rotate-master-key`, then log in with an existing test account (password,
  TOTP and a recovery code all still work), and `data/logs/` shows `master_key_rotated` and
  `master_key_deleted`.
- After stage 6: `ao3mirror backup /some/new/dir`, then restore it into a scratch config and
  log in.
