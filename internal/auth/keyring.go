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
// keys.db never goes into users.db, so a copy of users.db alone can't decrypt TOTP secrets,
// check recovery codes or guess passwords. Encryption always uses the active key and writes its
// ID into the ciphertext; decryption uses the key the ciphertext names. A Keyring is immutable.
type Keyring struct {
	active uint32
	keys   map[uint32]*keySet
}

// keySet is one master key's subkeys.
type keySet struct {
	totp         cipher.AEAD // XChaCha20-Poly1305
	recoveryHMAC []byte
	pepper       []byte // the password pepper; the PHC "k" parameter names its master key
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
	rec, err := derive("ao3mirror recovery-hmac")
	if err != nil {
		return nil, err
	}
	pepper, err := derive("ao3mirror password-pepper")
	if err != nil {
		return nil, err
	}
	totp, err := chacha20poly1305.NewX(encKey)
	if err != nil {
		return nil, err
	}
	return &keySet{totp: totp, recoveryHMAC: rec, pepper: pepper}, nil
}

// ActiveID is the ID of the master key new values are encrypted under.
func (r *Keyring) ActiveID() int64 { return int64(r.active) }

func (r *Keyring) activeSet() *keySet { return r.keys[r.active] }

// pepperKey returns the password pepper of the master key with the given ID, if held.
func (r *Keyring) pepperKey(id uint32) ([]byte, bool) {
	ks, ok := r.keys[id]
	if !ok {
		return nil, false
	}
	return ks.pepper, true
}

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
