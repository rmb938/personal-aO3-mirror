package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// KeyFileName is the master key's file name in the API's private directory (data/api, 0700).
const KeyFileName = "auth.key"

// keyFileBytes is the master key's length; the file holds exactly these raw bytes.
const keyFileBytes = 32

// totpCipherVersion is the first byte of an encrypted TOTP secret: version, then the 12-byte
// GCM nonce, then ciphertext and tag.
const totpCipherVersion = 1

// ErrTOTPDecrypt is a TOTP secret that fails to decrypt: a wrong key file, the wrong
// TOTPContext, or a corrupt row.
var ErrTOTPDecrypt = errors.New("auth: TOTP secret does not decrypt")

// Secrets are the keys derived from auth.key with HKDF-SHA256. auth.key never goes into
// users.db, so a copy of the database alone can't decrypt TOTP secrets or check recovery codes.
// Losing the file means every user needs a 2FA reset: back it up with users.db.
type Secrets struct {
	totp         cipher.AEAD
	recoveryHMAC []byte
	nameHMAC     []byte
}

// LoadOrCreateSecrets reads the master key at path, creating it (32 random bytes, 0600) when
// it doesn't exist. The file is created under a temporary name and linked into place, so a
// crash never leaves a short key and two processes starting at once end up with the same key.
// A key file readable by group or others, or of the wrong length, is an error.
func LoadOrCreateSecrets(path string) (*Secrets, error) {
	key, err := readKeyFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := createKeyFile(path); err != nil {
			return nil, err
		}
		key, err = readKeyFile(path)
	}
	if err != nil {
		return nil, err
	}
	return newSecrets(key)
}

func readKeyFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("auth: %s must be a regular file with mode 0600 (is %v)", path, fi.Mode())
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != keyFileBytes {
		return nil, fmt.Errorf("auth: %s holds %d bytes, want %d", path, len(key), keyFileBytes)
	}
	return key, nil
}

func createKeyFile(path string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*") // 0600
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.Remove(tmp.Name())) }()
	if _, err := tmp.Write(randomBytes(keyFileBytes)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link fails if another process got there first; its key is the one to use.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

func newSecrets(master []byte) (*Secrets, error) {
	derive := func(info string) ([]byte, error) { return hkdf.Key(sha256.New, master, nil, info, 32) }
	encKey, err := derive("ao3mirror totp-enc")
	if err != nil {
		return nil, err
	}
	rec, err := derive("ao3mirror recovery-hmac")
	if err != nil {
		return nil, err
	}
	name, err := derive("ao3mirror name-hmac")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Secrets{totp: gcm, recoveryHMAC: rec, nameHMAC: name}, nil
}

// TOTPContext binds an encrypted TOTP secret to the row it belongs to (the GCM additional
// data), so a secret copied to another row doesn't decrypt. A pending enrollment has no user
// yet, so its secret is bound to its invite; confirming re-encrypts it for the user.
type TOTPContext struct{ aad string }

// UserTOTP is the context of a user's TOTP secret.
func UserTOTP(userID int64) TOTPContext { return TOTPContext{"user:" + strconv.FormatInt(userID, 10)} }

// EnrollmentTOTP is the context of a pending signup or reset's TOTP secret.
func EnrollmentTOTP(inviteID int64) TOTPContext {
	return TOTPContext{"enrollment:" + strconv.FormatInt(inviteID, 10)}
}

// EncryptTOTPSecret seals secret with AES-256-GCM under a random nonce.
func (s *Secrets) EncryptTOTPSecret(c TOTPContext, secret []byte) []byte {
	out := append([]byte{totpCipherVersion}, randomBytes(s.totp.NonceSize())...)
	return s.totp.Seal(out, out[1:], secret, []byte(c.aad))
}

// DecryptTOTPSecret opens what EncryptTOTPSecret sealed with the same context.
func (s *Secrets) DecryptTOTPSecret(c TOTPContext, enc []byte) ([]byte, error) {
	n := s.totp.NonceSize()
	if len(enc) < 1+n+s.totp.Overhead() || enc[0] != totpCipherVersion {
		return nil, ErrTOTPDecrypt
	}
	secret, err := s.totp.Open(nil, enc[1:1+n], enc[1+n:], []byte(c.aad))
	if err != nil {
		return nil, ErrTOTPDecrypt
	}
	return secret, nil
}

// RecoveryCodeMAC is the stored form of a recovery code: HMAC-SHA256 of its normalized form
// (NormalizeRecoveryCode). The codes have 120 bits, so NIST SP 800-63B-4 §3.1.2.2 allows an
// approved hash rather than a password hashing scheme; it is keyed (with a key outside users.db)
// so a copy of users.db alone allows no guessing at all.
func (s *Secrets) RecoveryCodeMAC(normalized string) [32]byte {
	return mac32(s.recoveryHMAC, normalized)
}

// NameMAC keys per-username state that must not hold the username itself (the unknown-user
// login backoff). The caller passes the case-folded username.
func (s *Secrets) NameMAC(username string) [32]byte { return mac32(s.nameHMAC, username) }

func mac32(key []byte, msg string) [32]byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return [32]byte(m.Sum(nil))
}
