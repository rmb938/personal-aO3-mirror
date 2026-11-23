package auth

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"

	"golang.org/x/crypto/chacha20poly1305"
)

// MasterKeyBytes is a master key's length.
const MasterKeyBytes = 32

// MaxMasterKeyID is the largest master key ID: a ciphertext stores the ID in 4 bytes.
const MaxMasterKeyID = math.MaxUint32

// Ciphertext layout (Tink's): version(1) | key_id(4, big-endian) | nonce(24) | ciphertext+tag(16).
const (
	envelopeVersion = 2 // 1 was AES-256-GCM under auth.key, with no key ID
	envelopeHeader  = 1 + 4
	envelopeMin     = envelopeHeader + chacha20poly1305.NonceSizeX + chacha20poly1305.Overhead
)

var (
	// ErrKeyNotHeld is a ciphertext under a master key this server doesn't hold (a users.db
	// restored without its keys.db, or a key deleted too early).
	ErrKeyNotHeld = errors.New("auth: ciphertext is under a master key this server does not hold")
	// ErrDecrypt is a ciphertext that fails to decrypt: the wrong context (another row), or a
	// corrupt or tampered value.
	ErrDecrypt = errors.New("auth: ciphertext does not decrypt")
)

// MasterKey is one master key from keys.db.
type MasterKey struct {
	ID  int64
	Key []byte // MasterKeyBytes
}

// NewMasterKey returns a new random master key's bytes.
func NewMasterKey() []byte { return randomBytes(MasterKeyBytes) }

// Keyring holds the master keys (from keys.db) and the subkeys HKDF-SHA256 derives from each.
// keys.db never goes into users.db, so a copy of users.db alone can't decrypt TOTP secrets or
// signing keys, check recovery codes or guess passwords. Encryption always uses the active key and writes its
// ID into the ciphertext; decryption uses the key the ciphertext names. A Keyring is immutable.
type Keyring struct {
	active uint32
	keys   map[uint32]*keySet
}

// keySet is one master key's subkeys.
type keySet struct {
	totp         cipher.AEAD // XChaCha20-Poly1305
	signing      cipher.AEAD // XChaCha20-Poly1305, for the JWT signing seeds
	password     cipher.AEAD // XChaCha20-Poly1305, over Argon2id outputs; the PHC "k" names its key
	recoveryHMAC []byte
}

// NewKeyring returns a Keyring over keys, encrypting with the key whose ID is active. IDs must
// be distinct, from 1 to MaxMasterKeyID, and keys MasterKeyBytes long.
func NewKeyring(active int64, keys []MasterKey) (*Keyring, error) {
	r := &Keyring{keys: make(map[uint32]*keySet, len(keys))}
	for _, mk := range keys {
		if mk.ID < 1 || mk.ID > MaxMasterKeyID {
			return nil, fmt.Errorf("auth: master key ID %d out of range", mk.ID)
		}
		id := uint32(mk.ID)
		if _, dup := r.keys[id]; dup {
			return nil, fmt.Errorf("auth: master key ID %d twice", mk.ID)
		}
		ks, err := newKeySet(mk.Key)
		if err != nil {
			return nil, fmt.Errorf("auth: master key %d: %w", mk.ID, err)
		}
		r.keys[id] = ks
	}
	if active < 1 || active > MaxMasterKeyID || r.keys[uint32(active)] == nil {
		return nil, fmt.Errorf("auth: active master key %d is not in the keyring", active)
	}
	r.active = uint32(active)
	return r, nil
}

func newKeySet(master []byte) (*keySet, error) {
	if len(master) != MasterKeyBytes {
		return nil, fmt.Errorf("holds %d bytes, want %d", len(master), MasterKeyBytes)
	}
	derive := func(info string) ([]byte, error) { return hkdf.Key(sha256.New, master, nil, info, 32) }
	encKey, err := derive("ao3mirror totp-enc")
	if err != nil {
		return nil, err
	}
	signKey, err := derive("ao3mirror signing-key-enc")
	if err != nil {
		return nil, err
	}
	rec, err := derive("ao3mirror recovery-hmac")
	if err != nil {
		return nil, err
	}
	passKey, err := derive("ao3mirror password-enc")
	if err != nil {
		return nil, err
	}
	totp, err := chacha20poly1305.NewX(encKey)
	if err != nil {
		return nil, err
	}
	signing, err := chacha20poly1305.NewX(signKey)
	if err != nil {
		return nil, err
	}
	password, err := chacha20poly1305.NewX(passKey)
	if err != nil {
		return nil, err
	}
	return &keySet{totp: totp, signing: signing, password: password, recoveryHMAC: rec}, nil
}

// ActiveID is the ID of the master key new values are encrypted under.
func (r *Keyring) ActiveID() int64 { return int64(r.active) }

func (r *Keyring) activeSet() *keySet { return r.keys[r.active] }

// seal encrypts plaintext under the active key's AEAD (chosen by aead) with a random nonce.
// The additional data is the header (version and key ID) followed by aad, so neither the
// header nor the row the value belongs to can change undetected.
func (r *Keyring) seal(aead func(*keySet) cipher.AEAD, aad string, plaintext []byte) []byte {
	a := aead(r.activeSet())
	out := make([]byte, envelopeHeader, envelopeMin+len(plaintext))
	out[0] = envelopeVersion
	binary.BigEndian.PutUint32(out[1:envelopeHeader], r.active)
	ad := append(out[:envelopeHeader:envelopeHeader], aad...)
	out = append(out, randomBytes(a.NonceSize())...)
	return a.Seal(out, out[envelopeHeader:], plaintext, ad)
}

// open decrypts what seal wrote with the same aad, under the key the ciphertext names.
func (r *Keyring) open(aead func(*keySet) cipher.AEAD, aad string, enc []byte) ([]byte, error) {
	if len(enc) < envelopeMin || enc[0] != envelopeVersion {
		return nil, ErrDecrypt
	}
	ks, ok := r.keys[binary.BigEndian.Uint32(enc[1:envelopeHeader])]
	if !ok {
		return nil, ErrKeyNotHeld
	}
	a := aead(ks)
	ad := append(enc[:envelopeHeader:envelopeHeader], aad...)
	nonce := enc[envelopeHeader : envelopeHeader+a.NonceSize()]
	pt, err := a.Open(nil, nonce, enc[envelopeHeader+a.NonceSize():], ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func totpAEAD(ks *keySet) cipher.AEAD { return ks.totp }

// TOTPContext binds an encrypted TOTP secret to the row it belongs to (the additional data),
// so a secret copied to another row doesn't decrypt. A pending enrollment has no user yet, so
// its secret is bound to its invite; confirming re-encrypts it for the user.
type TOTPContext struct{ aad string }

// UserTOTP is the context of a user's TOTP secret.
func UserTOTP(userID int64) TOTPContext { return TOTPContext{"user:" + strconv.FormatInt(userID, 10)} }

// EnrollmentTOTP is the context of a pending signup or reset's TOTP secret.
func EnrollmentTOTP(inviteID int64) TOTPContext {
	return TOTPContext{"enrollment:" + strconv.FormatInt(inviteID, 10)}
}

// EncryptTOTPSecret encrypts secret with XChaCha20-Poly1305 under the active master key.
func (r *Keyring) EncryptTOTPSecret(c TOTPContext, secret []byte) []byte {
	return r.seal(totpAEAD, c.aad, secret)
}

// DecryptTOTPSecret opens what EncryptTOTPSecret sealed with the same context: ErrKeyNotHeld
// when its master key isn't in the keyring, ErrDecrypt when it doesn't open.
func (r *Keyring) DecryptTOTPSecret(c TOTPContext, enc []byte) ([]byte, error) {
	return r.open(totpAEAD, c.aad, enc)
}

func passwordAEAD(ks *keySet) cipher.AEAD { return ks.password }

// PasswordContext binds a wrapped password hash to the row it belongs to, as TOTPContext does
// for TOTP secrets: a pending enrollment's hash is bound to its invite, and confirming re-wraps
// it for the user (Keyring.RewrapPasswordHash).
type PasswordContext struct{ aad string }

// UserPassword is the context of a user's password hash.
func UserPassword(userID int64) PasswordContext {
	return PasswordContext{"user:" + strconv.FormatInt(userID, 10)}
}

// EnrollmentPassword is the context of a pending signup or reset's password hash.
func EnrollmentPassword(inviteID int64) PasswordContext {
	return PasswordContext{"enrollment:" + strconv.FormatInt(inviteID, 10)}
}

// dummyPassword is the context of the Hasher's dummy hash, which no row holds. The other
// contexts start with a word and a colon, so it can't equal one.
var dummyPassword = PasswordContext{"dummy"}

// wrapPasswordHash seals key (an Argon2id output) under the active master key into ph, which
// holds the parameters and salt: ph.keyID becomes the active ID and ph.sealed the nonce and
// ciphertext. The additional data is the header, c and the PHC string up to the salt, so
// neither the row nor the parameters, key ID or salt can change undetected.
func (r *Keyring) wrapPasswordHash(c PasswordContext, ph *phcHash, key []byte) {
	ph.keyID = r.active
	env := r.seal(passwordAEAD, c.aad+phcPrefix(*ph), key)
	ph.sealed = env[envelopeHeader:]
}

// unwrapPasswordHash opens what wrapPasswordHash sealed into ph with the same context:
// ErrKeyNotHeld when its master key isn't in the keyring, ErrDecrypt when it doesn't open.
func (r *Keyring) unwrapPasswordHash(c PasswordContext, ph phcHash) ([]byte, error) {
	env := make([]byte, envelopeHeader, envelopeHeader+len(ph.sealed))
	env[0] = envelopeVersion
	binary.BigEndian.PutUint32(env[1:envelopeHeader], ph.keyID)
	return r.open(passwordAEAD, c.aad+phcPrefix(ph), append(env, ph.sealed...))
}

// RewrapPasswordHash re-wraps the PHC string phc from context from to context to under the
// active master key. It runs no Argon2id (only the outer encryption changes), so it may run
// inside a transaction. A malformed phc is ErrMalformedHash; otherwise as unwrapPasswordHash.
func (r *Keyring) RewrapPasswordHash(from, to PasswordContext, phc string) (string, error) {
	ph, err := decodePHC(phc)
	if err != nil {
		return "", err
	}
	key, err := r.unwrapPasswordHash(from, ph)
	if err != nil {
		return "", err
	}
	r.wrapPasswordHash(to, &ph, key)
	return encodePHC(ph), nil
}

func signingAEAD(ks *keySet) cipher.AEAD { return ks.signing }

func signingAAD(kid string) string { return "signing-key:" + kid }

// EncryptSigningSeed encrypts an access-token signing key's Ed25519 seed with
// XChaCha20-Poly1305 under the active master key, bound to its kid, so a copy of users.db
// alone can't mint tokens.
func (r *Keyring) EncryptSigningSeed(kid string, seed []byte) []byte {
	return r.seal(signingAEAD, signingAAD(kid), seed)
}

// DecryptSigningSeed opens what EncryptSigningSeed sealed for the same kid: ErrKeyNotHeld
// when its master key isn't in the keyring, ErrDecrypt when it doesn't open.
func (r *Keyring) DecryptSigningSeed(kid string, enc []byte) ([]byte, error) {
	return r.open(signingAEAD, signingAAD(kid), enc)
}

// RecoveryCodeMAC is the stored form of a recovery code: HMAC-SHA256 of its normalized form
// (NormalizeRecoveryCode) under the active master key. The codes have 120 bits, so NIST SP
// 800-63B-4 §3.1.2.2 allows an approved hash rather than a password hashing scheme; it is keyed
// (with a key outside users.db) so a copy of users.db alone allows no guessing at all.
func (r *Keyring) RecoveryCodeMAC(normalized string) [32]byte {
	return mac32(r.activeSet().recoveryHMAC, normalized)
}

func mac32(key []byte, msg string) [32]byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return [32]byte(m.Sum(nil))
}
