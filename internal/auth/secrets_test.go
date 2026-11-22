package auth

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFileName)
	s1, err := LoadOrCreateSecrets(path)
	require.NoError(t, err)

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	require.EqualValues(t, keyFileBytes, fi.Size())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file left")

	// Loading again gives the same keys.
	s2, err := LoadOrCreateSecrets(path)
	require.NoError(t, err)
	enc := s1.EncryptTOTPSecret(UserTOTP(7), []byte("fixture secret"))
	got, err := s2.DecryptTOTPSecret(UserTOTP(7), enc)
	require.NoError(t, err)
	require.Equal(t, []byte("fixture secret"), got)
	require.Equal(t, s1.RecoveryCodeMAC("abcd"), s2.RecoveryCodeMAC("abcd"))
	require.Equal(t, s1.NameMAC("fixture_user"), s2.NameMAC("fixture_user"))

	// Another key file decrypts nothing and MACs differently.
	s3, err := LoadOrCreateSecrets(filepath.Join(t.TempDir(), KeyFileName))
	require.NoError(t, err)
	_, err = s3.DecryptTOTPSecret(UserTOTP(7), enc)
	require.ErrorIs(t, err, ErrTOTPDecrypt)
	require.NotEqual(t, s1.RecoveryCodeMAC("abcd"), s3.RecoveryCodeMAC("abcd"))
}

func TestLoadOrCreateSecretsConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), KeyFileName)
	var wg sync.WaitGroup
	secrets := make([]*Secrets, 8)
	for i := range secrets {
		wg.Go(func() {
			s, err := LoadOrCreateSecrets(path)
			if err == nil {
				secrets[i] = s
			}
		})
	}
	wg.Wait()
	for _, s := range secrets {
		require.NotNil(t, s)
		require.Equal(t, secrets[0].NameMAC("x"), s.NameMAC("x"), "every caller has the same key")
	}
}

func TestLoadSecretsRejectsBadFile(t *testing.T) {
	dir := t.TempDir()

	loose := filepath.Join(dir, "loose.key")
	require.NoError(t, os.WriteFile(loose, make([]byte, keyFileBytes), 0o600))
	require.NoError(t, os.Chmod(loose, 0o640)) //nolint:gosec // G302: the test needs a group-readable key file
	_, err := LoadOrCreateSecrets(loose)
	require.ErrorContains(t, err, "mode 0600")

	short := filepath.Join(dir, "short.key")
	require.NoError(t, os.WriteFile(short, make([]byte, keyFileBytes-1), 0o600))
	_, err = LoadOrCreateSecrets(short)
	require.ErrorContains(t, err, "want 32")

	_, err = LoadOrCreateSecrets(dir) // a directory
	require.Error(t, err)

	_, err = LoadOrCreateSecrets(filepath.Join(dir, "missing", KeyFileName))
	require.Error(t, err)
}

func TestTOTPSecretEncryption(t *testing.T) {
	s, err := LoadOrCreateSecrets(filepath.Join(t.TempDir(), KeyFileName))
	require.NoError(t, err)
	secret := NewTOTPSecret()

	enc := s.EncryptTOTPSecret(UserTOTP(1), secret)
	require.NotEqual(t, enc, s.EncryptTOTPSecret(UserTOTP(1), secret), "random nonce")
	got, err := s.DecryptTOTPSecret(UserTOTP(1), enc)
	require.NoError(t, err)
	require.Equal(t, secret, got)

	// The additional data binds the ciphertext to its row.
	for _, c := range []TOTPContext{UserTOTP(2), EnrollmentTOTP(1), {}} {
		_, err := s.DecryptTOTPSecret(c, enc)
		require.ErrorIs(t, err, ErrTOTPDecrypt, "%+v", c)
	}

	tampered := append([]byte(nil), enc...)
	tampered[len(tampered)-1] ^= 1
	_, err = s.DecryptTOTPSecret(UserTOTP(1), tampered)
	require.ErrorIs(t, err, ErrTOTPDecrypt)

	wrongVersion := append([]byte{2}, enc[1:]...)
	_, err = s.DecryptTOTPSecret(UserTOTP(1), wrongVersion)
	require.ErrorIs(t, err, ErrTOTPDecrypt)

	for _, short := range [][]byte{nil, {totpCipherVersion}, enc[:20]} {
		_, err = s.DecryptTOTPSecret(UserTOTP(1), short)
		require.ErrorIs(t, err, ErrTOTPDecrypt)
	}
}

func TestMACsAreDomainSeparated(t *testing.T) {
	s, err := LoadOrCreateSecrets(filepath.Join(t.TempDir(), KeyFileName))
	require.NoError(t, err)
	require.NotEqual(t, s.RecoveryCodeMAC("fixture"), s.NameMAC("fixture"))
	require.NotEqual(t, s.NameMAC("fixture_a"), s.NameMAC("fixture_b"))
}
