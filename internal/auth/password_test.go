package auth

import (
	"context"
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
	h, err := NewHasher(live(t, newTestKeyring(t)), HasherOptions{Params: cheap})
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

// newTestKeyring returns a Keyring holding one new master key, ID 1.
func newTestKeyring(t *testing.T) *Keyring {
	t.Helper()
	r, err := NewKeyring(1, []MasterKey{{ID: 1, Key: NewMasterKey()}})
	require.NoError(t, err)
	return r
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

// The wrapped hash field is 24 nonce + 32 hash + 16 tag = 72 bytes: 96 base64 characters.
var phcRE = regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4,k=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{96}$`)

func TestHasherDefaultParams(t *testing.T) {
	h, err := NewHasher(live(t, newTestKeyring(t)), HasherOptions{})
	require.NoError(t, err)
	ctx := t.Context()
	c := UserPassword(1)
	phc, err := h.Hash(ctx, c, "fixture horse staple")
	require.NoError(t, err)
	require.Regexp(t, phcRE, phc)
	ok, err := h.Verify(ctx, c, phc, "fixture horse staple")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, h.NeedsRehash(phc))
	require.Equal(t, DefaultArgon2, h.dummy.params)
	require.Len(t, h.dummyK, int(DefaultArgon2.KeyLen))
}

func TestHasherRoundTrip(t *testing.T) {
	h := newCheapHasher(t)
	ctx := t.Context()
	c := UserPassword(1)
	phc, err := h.Hash(ctx, c, cafeDecomposed) // NFC: cafeComposed
	require.NoError(t, err)

	other, err := h.Hash(ctx, c, cafeDecomposed)
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
		ok, err := h.Verify(ctx, c, phc, pw)
		require.NoError(t, err)
		require.Equal(t, want, ok, "%q", pw)
	}

	// NFC, not NFKC: a ligature is its own character.
	phc, err = h.Hash(ctx, c, "ﬁxture password")
	require.NoError(t, err)
	ok, err := h.Verify(ctx, c, phc, "fixture password")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, h.VerifyDummy(ctx, "fixture password"))
}

// TestHasherWrap: the stored hash is Argon2id of the NFC password (no pepper), encrypted under
// the active master key and bound to its row and its PHC parameters, so it only verifies with
// the same keys.db, for the same row, unaltered.
func TestHasherWrap(t *testing.T) {
	ctx := t.Context()
	key := NewMasterKey()
	keyring, err := NewKeyring(1, []MasterKey{{ID: 1, Key: key}})
	require.NoError(t, err)
	h, err := NewHasher(live(t, keyring), HasherOptions{Params: cheap})
	require.NoError(t, err)
	c := UserPassword(1)
	phc, err := h.Hash(ctx, c, cafeDecomposed)
	require.NoError(t, err)

	ph, err := decodePHC(phc)
	require.NoError(t, err)
	require.Equal(t, keyring.active, ph.keyID)
	require.Equal(t, cheap, ph.params)
	got, err := keyring.unwrapPasswordHash(c, ph)
	require.NoError(t, err)
	argon := argon2.IDKey([]byte(cafeComposed), ph.salt, cheap.Time, cheap.Memory, cheap.Threads, cheap.KeyLen)
	require.Equal(t, argon, got, "Argon2id of the NFC password, unpeppered")
	require.NotContains(t, string(ph.sealed), string(argon), "no hash bytes in the clear")

	// The same key bytes under the same ID verify (a restart reloads keys.db).
	again, err := NewKeyring(1, []MasterKey{{ID: 1, Key: key}})
	require.NoError(t, err)
	hAgain, err := NewHasher(live(t, again), HasherOptions{Params: cheap})
	require.NoError(t, err)
	ok, err := hAgain.Verify(ctx, c, phc, cafeDecomposed)
	require.NoError(t, err)
	require.True(t, ok)

	// Another keys.db with the same key ID: a server error, not a wrong password.
	other, err := NewHasher(live(t, newTestKeyring(t)), HasherOptions{Params: cheap})
	require.NoError(t, err)
	_, err = other.Verify(ctx, c, phc, cafeDecomposed)
	require.ErrorIs(t, err, ErrDecrypt)

	// A row copied to another user, or to an enrollment, doesn't unwrap.
	for _, wrong := range []PasswordContext{UserPassword(2), EnrollmentPassword(1), dummyPassword, {}} {
		_, err := h.Verify(ctx, wrong, phc, cafeDecomposed)
		require.ErrorIs(t, err, ErrDecrypt, "%q", wrong.aad)
	}

	// The parameters and salt are bound too: weakening the cost or swapping the salt fails.
	for _, edit := range []func(*phcHash){
		func(p *phcHash) { p.params.Time = 2 },
		func(p *phcHash) { p.params.Memory = 128 },
		func(p *phcHash) { p.salt = slices.Clone(p.salt); p.salt[0] ^= 1 },
		func(p *phcHash) { p.sealed = slices.Clone(p.sealed); p.sealed[len(p.sealed)-1] ^= 1 },
	} {
		bad := ph
		edit(&bad)
		_, err := h.Verify(ctx, c, encodePHC(bad), cafeDecomposed)
		require.ErrorIs(t, err, ErrDecrypt)
	}

	// A master key this server doesn't hold.
	unknown := ph
	unknown.keyID = 2
	_, err = h.Verify(ctx, c, encodePHC(unknown), cafeDecomposed)
	require.ErrorIs(t, err, ErrKeyNotHeld)
	require.False(t, h.NeedsRehash(encodePHC(unknown)), "the key ID is rotation's job, not login's")

	_, err = NewHasher(nil, HasherOptions{Params: cheap})
	require.Error(t, err)
}

// The dummy is wrapped at each use, so it still works once rotation has deleted the master key
// that was active when the Hasher was built.
func TestVerifyDummyAfterRotation(t *testing.T) {
	l := live(t, newTestKeyring(t))
	h, err := NewHasher(l, HasherOptions{Params: cheap})
	require.NoError(t, err)
	next, err := NewKeyring(2, []MasterKey{{ID: 2, Key: NewMasterKey()}})
	require.NoError(t, err)
	l.p.Store(next) // what stage 6's LiveKeyring.Store does, after key 1 is deleted
	require.NoError(t, h.VerifyDummy(t.Context(), "fixture password"))
}

// RewrapPasswordHash moves a hash from one row to another (an enrollment to its user) without
// the password, keeping the parameters and salt.
func TestRewrapPasswordHash(t *testing.T) {
	ctx := t.Context()
	keyring := newTestKeyring(t)
	h, err := NewHasher(live(t, keyring), HasherOptions{Params: cheap})
	require.NoError(t, err)
	from, to := EnrollmentPassword(3), UserPassword(9)
	phc, err := h.Hash(ctx, from, "fixture password")
	require.NoError(t, err)

	moved, err := keyring.RewrapPasswordHash(from, to, phc)
	require.NoError(t, err)
	require.NotEqual(t, phc, moved)
	before, err := decodePHC(phc)
	require.NoError(t, err)
	after, err := decodePHC(moved)
	require.NoError(t, err)
	require.Equal(t, before.params, after.params)
	require.Equal(t, before.salt, after.salt)

	ok, err := h.Verify(ctx, to, moved, "fixture password")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = h.Verify(ctx, to, moved, "fixture passwore")
	require.NoError(t, err)
	require.False(t, ok)
	_, err = h.Verify(ctx, from, moved, "fixture password")
	require.ErrorIs(t, err, ErrDecrypt)

	_, err = keyring.RewrapPasswordHash(to, from, phc) // the wrong source context
	require.ErrorIs(t, err, ErrDecrypt)
	_, err = keyring.RewrapPasswordHash(from, to, "garbage")
	require.ErrorIs(t, err, ErrMalformedHash)
	_, err = newTestKeyring(t).RewrapPasswordHash(from, to, phc)
	require.ErrorIs(t, err, ErrDecrypt)
}

// A hash under a master key that is still held but no longer active verifies and needs no
// rehash (rotation re-wraps it); re-wrapping moves it to the active key.
func TestHasherOlderMasterKey(t *testing.T) {
	ctx := t.Context()
	k1 := NewMasterKey()
	oldRing, err := NewKeyring(1, []MasterKey{{ID: 1, Key: k1}})
	require.NoError(t, err)
	newRing, err := NewKeyring(2, []MasterKey{{ID: 1, Key: k1}, {ID: 2, Key: NewMasterKey()}})
	require.NoError(t, err)
	oldH, err := NewHasher(live(t, oldRing), HasherOptions{Params: cheap})
	require.NoError(t, err)
	newH, err := NewHasher(live(t, newRing), HasherOptions{Params: cheap})
	require.NoError(t, err)

	c := UserPassword(1)
	phc, err := oldH.Hash(ctx, c, "fixture password")
	require.NoError(t, err)
	require.Contains(t, phc, ",k=1$")
	ok, err := newH.Verify(ctx, c, phc, "fixture password")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, newH.NeedsRehash(phc))

	rewrapped, err := newRing.RewrapPasswordHash(c, c, phc)
	require.NoError(t, err)
	require.Contains(t, rewrapped, ",k=2$")
	ok, err = newH.Verify(ctx, c, rewrapped, "fixture password")
	require.NoError(t, err)
	require.True(t, ok)
	_, err = oldH.Verify(ctx, c, rewrapped, "fixture password")
	require.ErrorIs(t, err, ErrKeyNotHeld)

	fresh, err := newH.Hash(ctx, c, "fixture password")
	require.NoError(t, err)
	require.Contains(t, fresh, ",k=2$")
}

func TestNeedsRehash(t *testing.T) {
	secrets := live(t, newTestKeyring(t))
	h, err := NewHasher(secrets, HasherOptions{Params: cheap})
	require.NoError(t, err)
	c := UserPassword(1)
	phc, err := h.Hash(t.Context(), c, "fixture password")
	require.NoError(t, err)
	require.False(t, h.NeedsRehash(phc))

	stronger := cheap
	stronger.Time = 2
	h2, err := NewHasher(secrets, HasherOptions{Params: stronger})
	require.NoError(t, err)
	require.True(t, h2.NeedsRehash(phc))
	// The old hash still verifies with its own parameters, so the login can rehash it.
	ok, err := h2.Verify(t.Context(), c, phc, "fixture password")
	require.NoError(t, err)
	require.True(t, ok)

	require.True(t, h.NeedsRehash("not a hash"))
}

func TestDecodePHC(t *testing.T) {
	const salt = "c2FsdHNhbHRzYWx0c2FsdA"
	// A wrapped 32-byte hash (72 bytes), and the shortest and longest allowed (8 and 64 bytes).
	key := phcB64.EncodeToString(make([]byte, 32+sealedOverhead))
	shortest := phcB64.EncodeToString(make([]byte, minArgon2Bytes+sealedOverhead))
	longest := phcB64.EncodeToString(make([]byte, maxArgon2Bytes+sealedOverhead))
	good := "$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key
	ph, err := decodePHC(good)
	require.NoError(t, err)
	require.Equal(t, Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, ph.params)
	require.Equal(t, uint32(1), ph.keyID)
	require.Len(t, ph.salt, 16)
	require.Len(t, ph.sealed, 32+sealedOverhead)
	require.Equal(t, good, encodePHC(ph))

	ph, err = decodePHC("$argon2id$v=19$m=64,t=1,p=1,k=4294967295$" + salt + "$" + key)
	require.NoError(t, err)
	require.Equal(t, uint32(1<<32-1), ph.keyID)
	for want, s := range map[uint32]string{minArgon2Bytes: shortest, maxArgon2Bytes: longest} {
		ph, err = decodePHC("$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + s)
		require.NoError(t, err)
		require.Equal(t, want, ph.params.KeyLen)
	}

	for _, bad := range []string{
		"",
		"$argon2i$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=16$m=64,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$t=1,m=64,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=064,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=+64,t=1,p=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,k=1$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt + "$" + key,                                         // no key ID: a stock Argon2id hash
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + phcB64.EncodeToString(make([]byte, 32)), // unwrapped
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + phcB64.EncodeToString(make([]byte, minArgon2Bytes-1+sealedOverhead)),
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$" + phcB64.EncodeToString(make([]byte, maxArgon2Bytes+1+sealedOverhead)),
		"$argon2id$v=19$m=64,t=1,p=1,k=1$" + salt + "$",
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
	_, err = h.Verify(t.Context(), UserPassword(1), "garbage", "fixture password")
	require.ErrorIs(t, err, ErrMalformedHash)
}

func TestNewHasherRejectsParams(t *testing.T) {
	secrets := live(t, newTestKeyring(t))
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
	h, err := NewHasher(live(t, newTestKeyring(t)), HasherOptions{Params: cheap, Concurrency: 1, MaxWait: 20 * time.Millisecond})
	require.NoError(t, err)
	h.sem <- struct{}{} // the only slot is taken

	start := time.Now()
	_, err = h.Hash(t.Context(), UserPassword(1), "fixture password")
	require.ErrorIs(t, err, ErrHasherBusy)
	require.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, h.VerifyDummy(ctx, "fixture password"), context.Canceled)

	<-h.sem
	_, err = h.Hash(t.Context(), UserPassword(1), "fixture password")
	require.NoError(t, err)
}
