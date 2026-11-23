package auth

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// cheap keeps the hashing tests fast; TestHasherDefaultParams covers the real cost.
var cheap = Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func newCheapHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher(newTestSecrets(t), HasherOptions{Params: cheap})
	require.NoError(t, err)
	return h
}

// Two spellings of "é", built from code points so the source shows which is which: decomposed
// (e, U+0301 COMBINING ACUTE ACCENT) and composed (U+00E9, its NFC form).
var (
	eDecomposed    = "e" + string(rune(0x0301))
	eComposed      = string(rune(0x00e9))
	cafeDecomposed = "caf" + eDecomposed + " fixture"
	cafeComposed   = "caf" + eComposed + " fixture"
)

// newTestSecrets returns Secrets from a new auth.key in a temp dir.
func newTestSecrets(t *testing.T) *Secrets {
	t.Helper()
	s, err := LoadOrCreateSecrets(filepath.Join(t.TempDir(), KeyFileName))
	require.NoError(t, err)
	return s
}

func TestCheckPassword(t *testing.T) {
	for _, tc := range []struct {
		name, password, username string
		want                     error
	}{
		{"ok", "fixture horse staple", "fixture_user", nil},
		{"min length", "fx7Qa9zL", "", nil},
		{"max length", strings.Repeat("q", MaxPasswordRunes), "", nil},
		{"unicode counts runes", strings.Repeat("é", MinPasswordRunes), "", nil},
		{"too short", "fx7Qa9z", "", ErrPasswordTooShort},
		{"too long", strings.Repeat("q", MaxPasswordRunes+1), "", ErrPasswordTooLong},
		// NFC composes eDecomposed into eComposed, so 16 runes become 8 (or 14 become 7).
		{"length after NFC", strings.Repeat(eDecomposed, 8), "", nil},
		{"too short after NFC", strings.Repeat(eDecomposed, 7), "", ErrPasswordTooShort},
		// NFC keeps the ligature "ﬃ" (NFKC would make it "ffi"), so 6 runes stay 6.
		{"no compatibility mapping", "ﬃxture", "", ErrPasswordTooShort},
		{"control character", "fixture\x00horse", "", ErrPasswordInvalid},
		{"newline", "fixture\nhorse", "", ErrPasswordInvalid},
		{"invalid UTF-8", "fixture\xffhorse", "", ErrPasswordInvalid},
		{"equals username", "Fixture_User_9", "fixture_user_9", ErrPasswordIsUsername},
		{"username folds compatibility", "ＦＩＸＴＵＲＥ＿ＵＳＥＲ", "fixture_user", ErrPasswordIsUsername},
		{"common", "password", "", ErrPasswordCommon},
		{"common ignores case", "PassWord", "", ErrPasswordCommon},
		// The list check folds with NFKC, looser than the NFC the password is hashed in.
		{"common folds compatibility", "ｐａｓｓｗｏｒｄ", "", ErrPasswordCommon}, // fullwidth letters
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, CheckPassword(tc.password, tc.username), tc.want)
			if tc.want == nil {
				require.NoError(t, CheckPassword(tc.password, tc.username))
			}
		})
	}
}

// TestCommonPasswordList checks the embedded list is in the form isCommonPassword searches:
// sorted, unique, folded (foldPassword: NFKC lower case), within the policy's length range.
func TestCommonPasswordList(t *testing.T) {
	list := commonPasswords()
	require.Greater(t, len(list), 40000)
	require.True(t, slices.IsSorted(list))
	for i, p := range list {
		if i > 0 {
			require.NotEqual(t, list[i-1], p)
		}
		require.Equal(t, strings.ToLower(norm.NFKC.String(p)), p)
		n := utf8.RuneCountInString(p)
		require.True(t, n >= MinPasswordRunes && n <= MaxPasswordRunes, "length %d", n)
	}
}

var phcRE = regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4,k=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)

func TestHasherDefaultParams(t *testing.T) {
	h, err := NewHasher(newTestSecrets(t), HasherOptions{})
	require.NoError(t, err)
	ctx := t.Context()
	phc, err := h.Hash(ctx, "fixture horse staple")
	require.NoError(t, err)
	require.Regexp(t, phcRE, phc)
	ok, err := h.Verify(ctx, phc, "fixture horse staple")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, h.NeedsRehash(phc))
	require.Regexp(t, phcRE, h.dummy)
}

func TestHasherRoundTrip(t *testing.T) {
	h := newCheapHasher(t)
	ctx := t.Context()
	phc, err := h.Hash(ctx, cafeDecomposed) // NFC: cafeComposed
	require.NoError(t, err)

	other, err := h.Hash(ctx, cafeDecomposed)
	require.NoError(t, err)
	require.NotEqual(t, phc, other, "random salt")

	for pw, want := range map[string]bool{
		cafeDecomposed:                 true,
		cafeComposed:                   true, // the NFC form
		"Caf" + eComposed + " fixture": false,
		"cafe fixture":                 false,
		cafeComposed + " ":             false,
		"":                             false,
	} {
		ok, err := h.Verify(ctx, phc, pw)
		require.NoError(t, err)
		require.Equal(t, want, ok, "%q", pw)
	}

	// NFC, not NFKC: a ligature is its own character.
	phc, err = h.Hash(ctx, "ﬁxture password")
	require.NoError(t, err)
	ok, err := h.Verify(ctx, phc, "fixture password")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, h.VerifyDummy(ctx, "fixture password"))
}

// TestHasherPepper: the stored key is Argon2id of the HMAC of the NFC password under auth.key's
// pepper, not of the password itself, so a hash only verifies with the same auth.key.
func TestHasherPepper(t *testing.T) {
	ctx := t.Context()
	secrets := newTestSecrets(t)
	h, err := NewHasher(secrets, HasherOptions{Params: cheap})
	require.NoError(t, err)
	phc, err := h.Hash(ctx, cafeDecomposed)
	require.NoError(t, err)

	ph, err := decodePHC(phc)
	require.NoError(t, err)
	require.Equal(t, uint32(currentPepper), ph.pepper)
	peppered := mac32(secrets.pepper, cafeComposed)
	require.Equal(t, argon2.IDKey(peppered[:], ph.salt, cheap.Time, cheap.Memory, cheap.Threads, cheap.KeyLen), ph.key)
	require.NotEqual(t, argon2.IDKey([]byte(cafeComposed), ph.salt, cheap.Time, cheap.Memory, cheap.Threads, cheap.KeyLen), ph.key)

	// Another auth.key: the same users.db row no longer verifies.
	other, err := NewHasher(newTestSecrets(t), HasherOptions{Params: cheap})
	require.NoError(t, err)
	ok, err := other.Verify(ctx, phc, cafeDecomposed)
	require.NoError(t, err)
	require.False(t, ok)

	// The pepper is its own HKDF subkey.
	require.NotEqual(t, secrets.pepper, secrets.recoveryHMAC)
	require.NotEqual(t, secrets.pepper, secrets.nameHMAC)
	require.Len(t, secrets.pepper, 32)

	// A hash naming a pepper key this server doesn't hold can't be checked, and needs a rehash.
	ph.pepper = 2
	unknown := encodePHC(ph)
	_, err = h.Verify(ctx, unknown, cafeDecomposed)
	require.ErrorIs(t, err, ErrMalformedHash)
	require.True(t, h.NeedsRehash(unknown))

	_, err = NewHasher(nil, HasherOptions{Params: cheap})
	require.Error(t, err)
}

func TestNeedsRehash(t *testing.T) {
	secrets := newTestSecrets(t)
	h, err := NewHasher(secrets, HasherOptions{Params: cheap})
	require.NoError(t, err)
	phc, err := h.Hash(t.Context(), "fixture password")
	require.NoError(t, err)
	require.False(t, h.NeedsRehash(phc))

	stronger := cheap
	stronger.Time = 2
	h2, err := NewHasher(secrets, HasherOptions{Params: stronger})
	require.NoError(t, err)
	require.True(t, h2.NeedsRehash(phc))
	// The old hash still verifies with its own parameters, so the login can rehash it.
	ok, err := h2.Verify(t.Context(), phc, "fixture password")
	require.NoError(t, err)
	require.True(t, ok)

	require.True(t, h.NeedsRehash("not a hash"))
}

func TestDecodePHC(t *testing.T) {
	const salt, key = "c2FsdHNhbHRzYWx0c2FsdA", "a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	good := "$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key
	ph, err := decodePHC(good)
	require.NoError(t, err)
	require.Equal(t, Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, ph.params)
	require.Equal(t, uint32(1), ph.pepper)
	require.Len(t, ph.salt, 16)
	require.Len(t, ph.key, 32)
	require.Equal(t, good, encodePHC(ph))

	ph, err = decodePHC("$argon2id$v=19$m=64,t=1,p=1,k=4294967295$" + salt + "$" + key)
	require.NoError(t, err)
	require.Equal(t, uint32(1<<32-1), ph.pepper)

	for _, bad := range []string{
		"",
		"$argon2i$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=16$m=64,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$t=1,m=64,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=064,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=+64,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt + "$" + key, // no pepper: a stock Argon2id hash
		"$argon2id$v=19$m=64,t=1,p=1,k=0$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1,k=01$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1,k=4294967296$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,k=1,p=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1,x=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1,k=1,x=1$" + salt + "$" + key,
		"$argon2id$v=19$m=4294967295,t=1,p=1,k=1$" + salt + "$" + key, // over the memory bound
		"$argon2id$v=19$m=64,t=17,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=0,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=0,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=300,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "==$" + key,                      // padding
		"$argon2id$v=19$m=64,t=1,p=1,k=1$c2Fsd$" + key,                               // salt too short
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt[:4] + "\n" + salt[4:] + "$" + key,  // base64 skips LF
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key[:8] + "\r\n" + key[8:], // and CR
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key + "$",
		"x$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key,
	} {
		_, err := decodePHC(bad)
		require.ErrorIs(t, err, ErrMalformedHash, "%q", bad)
	}

	h := newCheapHasher(t)
	_, err = h.Verify(t.Context(), "garbage", "fixture password")
	require.ErrorIs(t, err, ErrMalformedHash)
}

func TestNewHasherRejectsParams(t *testing.T) {
	secrets := newTestSecrets(t)
	for _, p := range []Argon2Params{
		{Memory: 64, Time: 0, Threads: 1, SaltLen: 16, KeyLen: 32},
		{Memory: 4, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32},
		{Memory: 64, Time: 1, Threads: 1, SaltLen: 4, KeyLen: 32},
		{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 128},
	} {
		_, err := NewHasher(secrets, HasherOptions{Params: p})
		require.Error(t, err, "%+v", p)
	}
}

func TestHasherBusy(t *testing.T) {
	h, err := NewHasher(newTestSecrets(t), HasherOptions{Params: cheap, Concurrency: 1, MaxWait: 20 * time.Millisecond})
	require.NoError(t, err)
	h.sem <- struct{}{} // the only slot is taken

	start := time.Now()
	_, err = h.Hash(t.Context(), "fixture password")
	require.ErrorIs(t, err, ErrHasherBusy)
	require.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = h.Verify(ctx, h.dummy, "fixture password")
	require.ErrorIs(t, err, context.Canceled)

	<-h.sem
	_, err = h.Hash(t.Context(), "fixture password")
	require.NoError(t, err)
}
