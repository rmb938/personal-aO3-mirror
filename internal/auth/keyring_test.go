package auth

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewKeyringValidates(t *testing.T) {
	k1, k2 := NewMasterKey(), NewMasterKey()
	require.Len(t, k1, MasterKeyBytes)
	require.NotEqual(t, k1, k2)

	r, err := NewKeyring(2, []MasterKey{{ID: 1, Key: k1}, {ID: 2, Key: k2}})
	require.NoError(t, err)
	require.EqualValues(t, 2, r.ActiveID())

	for name, tc := range map[string]struct {
		active int64
		keys   []MasterKey
	}{
		"no keys":           {1, nil},
		"active not held":   {3, []MasterKey{{ID: 1, Key: k1}}},
		"duplicate ID":      {1, []MasterKey{{ID: 1, Key: k1}, {ID: 1, Key: k2}}},
		"ID zero":           {1, []MasterKey{{ID: 0, Key: k1}, {ID: 1, Key: k2}}},
		"ID over uint32":    {1, []MasterKey{{ID: 1, Key: k1}, {ID: MaxMasterKeyID + 1, Key: k2}}},
		"active over range": {MaxMasterKeyID + 1, []MasterKey{{ID: 1, Key: k1}}},
		"short key":         {1, []MasterKey{{ID: 1, Key: k1[:31]}}},
	} {
		_, err := NewKeyring(tc.active, tc.keys)
		require.Error(t, err, name)
	}
}

func TestTOTPSecretEncryption(t *testing.T) {
	key := NewMasterKey()
	r, err := NewKeyring(7, []MasterKey{{ID: 7, Key: key}})
	require.NoError(t, err)
	secret := NewTOTPSecret()

	enc := r.EncryptTOTPSecret(UserTOTP(1), secret)
	require.NotEqual(t, enc, r.EncryptTOTPSecret(UserTOTP(1), secret), "random nonce")
	require.Len(t, enc, envelopeMin+len(secret))
	require.Equal(t, byte(envelopeVersion), enc[0])
	require.Equal(t, uint32(7), binary.BigEndian.Uint32(enc[1:5]), "the key ID, big-endian")
	got, err := r.DecryptTOTPSecret(UserTOTP(1), enc)
	require.NoError(t, err)
	require.Equal(t, secret, got)

	// The same key bytes under the same ID decrypt (a restart reloads keys.db).
	again, err := NewKeyring(7, []MasterKey{{ID: 7, Key: key}})
	require.NoError(t, err)
	got, err = again.DecryptTOTPSecret(UserTOTP(1), enc)
	require.NoError(t, err)
	require.Equal(t, secret, got)

	// The additional data binds the ciphertext to its row.
	for _, c := range []TOTPContext{UserTOTP(2), EnrollmentTOTP(1), {}} {
		_, err := r.DecryptTOTPSecret(c, enc)
		require.ErrorIs(t, err, ErrDecrypt, "%+v", c)
	}

	tampered := append([]byte(nil), enc...)
	tampered[len(tampered)-1] ^= 1
	_, err = r.DecryptTOTPSecret(UserTOTP(1), tampered)
	require.ErrorIs(t, err, ErrDecrypt)

	wrongVersion := append([]byte{1}, enc[1:]...)
	_, err = r.DecryptTOTPSecret(UserTOTP(1), wrongVersion)
	require.ErrorIs(t, err, ErrDecrypt)

	for _, short := range [][]byte{nil, {envelopeVersion}, enc[:envelopeMin-1]} {
		_, err = r.DecryptTOTPSecret(UserTOTP(1), short)
		require.ErrorIs(t, err, ErrDecrypt)
	}
}

func TestKeyringKeyIDs(t *testing.T) {
	k1, k2 := NewMasterKey(), NewMasterKey()
	old, err := NewKeyring(1, []MasterKey{{ID: 1, Key: k1}})
	require.NoError(t, err)
	both, err := NewKeyring(2, []MasterKey{{ID: 1, Key: k1}, {ID: 2, Key: k2}})
	require.NoError(t, err)
	onlyNew, err := NewKeyring(2, []MasterKey{{ID: 2, Key: k2}})
	require.NoError(t, err)
	secret := []byte("fixture secret")

	// A value under the old key still decrypts while the keyring holds it; new values use the
	// active key.
	encOld := old.EncryptTOTPSecret(UserTOTP(1), secret)
	got, err := both.DecryptTOTPSecret(UserTOTP(1), encOld)
	require.NoError(t, err)
	require.Equal(t, secret, got)
	encNew := both.EncryptTOTPSecret(UserTOTP(1), secret)
	require.Equal(t, uint32(2), binary.BigEndian.Uint32(encNew[1:5]))

	// Once the old key is gone, its values are "key not held", not a decrypt failure.
	_, err = onlyNew.DecryptTOTPSecret(UserTOTP(1), encOld)
	require.ErrorIs(t, err, ErrKeyNotHeld)

	// Another keyring with the same ID but other key bytes (a different keys.db).
	other, err := NewKeyring(1, []MasterKey{{ID: 1, Key: NewMasterKey()}})
	require.NoError(t, err)
	_, err = other.DecryptTOTPSecret(UserTOTP(1), encOld)
	require.ErrorIs(t, err, ErrDecrypt)

	// The key ID is authenticated: pointing it at another held key fails, at an unheld one is
	// "key not held".
	retarget := append([]byte(nil), encOld...)
	binary.BigEndian.PutUint32(retarget[1:5], 2)
	_, err = both.DecryptTOTPSecret(UserTOTP(1), retarget)
	require.ErrorIs(t, err, ErrDecrypt)
	retarget[4] = 9
	_, err = both.DecryptTOTPSecret(UserTOTP(1), retarget)
	require.ErrorIs(t, err, ErrKeyNotHeld)
}

func TestRecoveryCodeMAC(t *testing.T) {
	key := NewMasterKey()
	r1, err := NewKeyring(1, []MasterKey{{ID: 1, Key: key}})
	require.NoError(t, err)
	r2, err := NewKeyring(1, []MasterKey{{ID: 1, Key: key}})
	require.NoError(t, err)
	require.Equal(t, r1.RecoveryCodeMAC("abcd"), r2.RecoveryCodeMAC("abcd"), "same key, same MAC")
	require.NotEqual(t, r1.RecoveryCodeMAC("abcd"), r1.RecoveryCodeMAC("abce"))
	require.NotEqual(t, r1.RecoveryCodeMAC("abcd"), newTestKeyring(t).RecoveryCodeMAC("abcd"))

	// Each use has its own HKDF subkey.
	ks := r1.activeSet()
	require.NotEqual(t, ks.recoveryHMAC, ks.pepper)
}
