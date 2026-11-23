package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"
)

// The fuzz targets here check the auth parsers against oracles written from the documented
// rules (the doc comments and docs/part2-api.md stage 16), not by calling the parser.

// tokenReasons is the closed set of TokenReason values.
var tokenReasons = []TokenReason{
	TokenMalformed, TokenBadAlg, TokenUnknownKey, TokenBadSig, TokenExpired, TokenNotYetValid, TokenBadClaims,
}

// FuzzVerifier presents arbitrary strings and mutations of issued tokens (a byte replaced,
// a string inserted, or a cut with a string appended) to a Verifier that knows two keys. A
// token is accepted only when it is byte-identical to one that was issued, with that token's
// claims; every refusal is a *TokenError with a listed reason.
func FuzzVerifier(f *testing.F) {
	now := jwtNow
	k1, k2, unknown := NewSigningKey(), NewSigningKey(), NewSigningKey()
	type issued struct {
		token string
		c     AccessClaims
	}
	var toks []issued
	for i, k := range []SigningKey{k1, k2, unknown} {
		tok, c, err := NewIssuer(testIss, k, 15*time.Minute, fixedClock(now)).Issue(int64(i+1), int64(i+10))
		require.NoError(f, err)
		toks = append(toks, issued{tok, c})
	}
	tok, c, err := NewIssuer("https://other.example.com", k1, 15*time.Minute, fixedClock(now)).Issue(4, 40)
	require.NoError(f, err)
	toks = append(toks, issued{tok, c})
	v := NewVerifier(testIss, []VerifyKey{k1.VerifyKey(), k2.VerifyKey()}, fixedClock(now.Add(time.Minute)))
	accepted := map[string]AccessClaims{toks[0].token: toks[0].c, toks[1].token: toks[1].c}

	for _, s := range []string{"", ".", "..", "a.b.c", "\n", "\r", "=", "==", " ", "\x00", "eyJ", toks[0].token} {
		for mode := range uint8(4) {
			f.Add(mode, uint8(0), 0, uint8('A'), s)
			f.Add(mode, uint8(1), len(toks[1].token)-3, uint8('\n'), s)
		}
	}
	for i, t := range toks {
		dot := strings.LastIndexByte(t.token, '.')
		f.Add(uint8(1), uint8(i), dot+1, uint8('\n'), "")
		f.Add(uint8(2), uint8(i), dot+5, uint8(0), "\n")
		f.Add(uint8(2), uint8(i), len(t.token), uint8(0), "=")
		f.Add(uint8(3), uint8(i), dot, uint8(0), ".")
	}
	f.Fuzz(func(t *testing.T, mode, which uint8, pos int, b byte, s string) {
		base := toks[int(which)%len(toks)].token
		p := pos % (len(base) + 1)
		if p < 0 {
			p += len(base) + 1
		}
		cand := s
		switch mode % 4 {
		case 1: // replace one byte
			if p < len(base) {
				cand = base[:p] + string([]byte{b}) + base[p+1:]
			}
		case 2: // insert s
			cand = base[:p] + s + base[p:]
		case 3: // cut, then append s
			cand = base[:p] + s
		}
		got, err := v.Verify(cand)
		if want, ok := accepted[cand]; ok {
			require.NoError(t, err)
			require.Equal(t, want.UserID, got.UserID)
			require.Equal(t, want.SessionID, got.SessionID)
			require.Equal(t, want.ID, got.ID)
			require.True(t, want.IssuedAt.Equal(got.IssuedAt) && want.ExpiresAt.Equal(got.ExpiresAt))
			return
		}
		require.Error(t, err, "accepted a token that was never issued: %q", cand)
		te, ok := errors.AsType[*TokenError](err)
		require.True(t, ok, "want *TokenError, got %T", err)
		require.Contains(t, tokenReasons, te.Reason)
	})
}

// FuzzPHC checks decodePHC both ways. Any string decodes only when it is canonical (encodePHC
// gives it back), its parameters are within the maxArgon2* bounds (so a corrupt row can't make
// Verify allocate more than 1 GiB) and it names a pepper key ID (k ≥ 1). Values encoded by
// encodePHC decode exactly when they are within those bounds.
func FuzzPHC(f *testing.F) {
	salt16, key32 := make([]byte, 16), make([]byte, 32)
	good := encodePHC(phcHash{params: DefaultArgon2, pepper: 1, salt: salt16, key: key32})
	for _, s := range []string{
		good, "", "$", "$argon2id$v=19$m=65536,t=3,p=4,k=1$$", strings.Replace(good, "m=65536", "m=065536", 1),
		strings.Replace(good, "m=65536", "m=1048577", 1), strings.Replace(good, "m=65536", "m=+65536", 1),
		strings.Replace(good, "t=3", "t=0", 1), strings.Replace(good, "p=4", "p=17", 1),
		strings.Replace(good, ",k=1", "", 1), strings.Replace(good, "k=1", "k=0", 1),
		strings.Replace(good, "k=1", "k=4294967296", 1), strings.Replace(good, "k=1", "k=01", 1),
		strings.Replace(good, "$argon2id", "$argon2i", 1), strings.Replace(good, "v=19", "v=16", 1),
		strings.Replace(good, "m=65536,t=3,p=4", "t=3,m=65536,p=4", 1), good + "=", good + "\n",
		good[:len(good)-10] + "\n" + good[len(good)-10:], good[:len(good)-1] + "B", good + "$",
	} {
		f.Add(s, uint32(65536), uint32(3), uint8(4), uint32(1), salt16, key32)
	}
	f.Add("", uint32(7), uint32(1), uint8(1), uint32(0), salt16[:8], key32[:8])
	f.Add("", uint32(1<<20+1), uint32(17), uint8(0), uint32(1), salt16[:7], slices.Concat(key32, key32))
	f.Add("", uint32(64), uint32(16), uint8(8), uint32(1<<32-1), slices.Concat(salt16, salt16), key32)
	f.Fuzz(func(t *testing.T, s string, m, tm uint32, th uint8, pepper uint32, salt, key []byte) {
		if ph, err := decodePHC(s); err == nil {
			p := ph.params
			require.Equal(t, s, encodePHC(ph), "only a canonical PHC string decodes")
			require.True(t, inArgon2Bounds(p.Memory, p.Time, uint32(p.Threads), len(ph.salt), len(ph.key)), "%+v", p)
			require.NotZero(t, ph.pepper)
			require.Equal(t, len(ph.salt), int(p.SaltLen))
			require.Equal(t, len(ph.key), int(p.KeyLen))
		} else {
			require.ErrorIs(t, err, ErrMalformedHash)
		}

		in := phcHash{
			params: Argon2Params{Memory: m, Time: tm, Threads: th, SaltLen: uint32(len(salt)), KeyLen: uint32(len(key))}, //nolint:gosec // G115: fuzz input lengths are small
			pepper: pepper, salt: salt, key: key,
		}
		got, err := decodePHC(encodePHC(in))
		if !inArgon2Bounds(m, tm, uint32(th), len(salt), len(key)) || pepper == 0 {
			require.ErrorIs(t, err, ErrMalformedHash)
			return
		}
		require.NoError(t, err)
		require.Equal(t, in.params, got.params)
		require.Equal(t, pepper, got.pepper)
		require.Equal(t, salt, got.salt)
		require.Equal(t, key, got.key)
	})
}

// inArgon2Bounds is the documented range of stored parameters: memory 8×threads KiB to 1 GiB,
// time 1..16, threads 1..16, salt and key 8..64 bytes.
func inArgon2Bounds(m, t, th uint32, salt, key int) bool {
	return th >= 1 && th <= 16 && m >= 8*th && m <= 1<<20 && t >= 1 && t <= 16 &&
		salt >= 8 && salt <= 64 && key >= 8 && key <= 64
}

// opaqueRE is an opaque token's body: 43 base64url characters whose last holds 4 bits (the
// other 2 zero), so 32 bytes encode exactly one way.
var opaqueRE = regexp.MustCompile(`\A[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]\z`)

// FuzzTokenSum: a token is accepted for kind k only with k's prefix and a canonical 43-character
// base64url body, and its sum is the SHA-256 of the whole token.
func FuzzTokenSum(f *testing.F) {
	kinds := []TokenKind{InviteToken, EnrollmentToken, ChallengeToken, RefreshToken}
	for i, k := range kinds {
		tok, _ := NewToken(k)
		for _, s := range []string{
			tok, tok[:len(tok)-1], tok + "A", tok[:len(tok)-1] + "B", strings.ToUpper(tok),
			tok[:10] + "\n" + tok[11:], tok[:10] + "+" + tok[11:], tok[:len(tok)-2] + "==", string(k),
			strings.Replace(tok, string(k), string(kinds[(i+1)%len(kinds)]), 1), "",
		} {
			f.Add(uint8(i), s)
		}
	}
	f.Fuzz(func(t *testing.T, which uint8, tok string) {
		k := kinds[int(which)%len(kinds)]
		sum, ok := TokenSum(k, tok)
		body, hasPrefix := strings.CutPrefix(tok, string(k))
		want := hasPrefix && opaqueRE.MatchString(body)
		require.Equal(t, want, ok, "%s %q", k, tok)
		if ok {
			require.Equal(t, sha256.Sum256([]byte(tok)), sum)
			b, err := base64.RawURLEncoding.DecodeString(body)
			require.NoError(t, err)
			require.Len(t, b, 32)
		} else {
			require.Equal(t, [32]byte{}, sum)
		}
	})
}

// FuzzNormalizeRecoveryCode: a typed code of at most 96 bytes is accepted only when, with ASCII
// dashes and spaces removed and ASCII letters lower-cased, it is exactly 24 characters of
// [a-z2-7]; the result is that form.
func FuzzNormalizeRecoveryCode(f *testing.F) {
	for _, s := range append(NewRecoveryCodes(),
		"abcd-efgh-ijkl-mnop-qrst-uv23", " ABCD EFGH-IJKL-MNOP QRST-UV23 ", "abcdefghijklmnopqrstuv23",
		"abcd-efgh-ijkl-mnop-qrst-uv2", "abcd-efgh-ijkl-mnop-qrst-uv231", "abcd-efgh-ijkl-mn23",
		"abcd-efgh-ijkl-mnop-qrst-uv01", "abcd\tefgh-ijkl-mnop-qrst-uv23", "abcd\nefgh-ijkl-mnop-qrst-uv23",
		"abcdefghijklmnopqrstuv2K", "",
		strings.Repeat("-", 72)+"abcdefghijklmnopqrstuv23", strings.Repeat("-", 73)+"abcdefghijklmnopqrstuv23",
	) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, code string) {
		got, ok := NormalizeRecoveryCode(code)
		var want []byte
		valid := len(code) <= 96
		for i := 0; i < len(code) && valid; i++ {
			switch c := code[i]; {
			case c == '-' || c == ' ':
			case c >= 'A' && c <= 'Z':
				want = append(want, c-'A'+'a')
			case c >= 'a' && c <= 'z' || c >= '2' && c <= '7':
				want = append(want, c)
			default:
				valid = false
			}
		}
		valid = valid && len(want) == 24
		require.Equal(t, valid, ok, "%q", code)
		if ok {
			require.Equal(t, string(want), got)
		} else {
			require.Empty(t, got)
		}
	})
}

// FuzzVerifyTOTP: a code is accepted only when it is 6 ASCII digits and the code of a step
// within one of now that is after lastStep (the earliest such step is returned). The code for
// now is always accepted when now's step is after lastStep.
func FuzzVerifyTOTP(f *testing.F) {
	secret := []byte("fixture-totp-secret!")
	now := jwtNow.Unix()
	cur := now / 30
	for _, s := range []string{TOTPCode(secret, jwtNow), "000000", "12345", "1234567", "١٢٣٤٥٦", "12345\n", " 12345", "+12345"} {
		for _, last := range []int64{0, cur - 2, cur - 1, cur, cur + 1} {
			f.Add(secret, s, now, last)
		}
	}
	f.Add([]byte{}, "000000", int64(0), int64(0))
	f.Fuzz(func(t *testing.T, secret []byte, code string, unix, last int64) {
		unix = 30 + unix%(1<<40) // a positive time, so every candidate step is positive
		if unix < 30 {
			unix += 1 << 40
		}
		at := time.Unix(unix, 0)
		cur := unix / 30
		step, ok := VerifyTOTP(secret, code, at, last)
		if !ok {
			require.Zero(t, step)
			if cur > last {
				require.NotEqual(t, TOTPCode(secret, at), code, "now's code is accepted")
			}
			return
		}
		require.Len(t, code, 6)
		require.Regexp(t, `\A[0-9]{6}\z`, code)
		require.True(t, step >= cur-1 && step <= cur+1 && step > last, "step %d, now %d, last %d", step, cur, last)
		require.Equal(t, TOTPCode(secret, time.Unix(step*30, 0)), code)
		for s := max(cur-1, last+1); s < step; s++ {
			require.NotEqual(t, TOTPCode(secret, time.Unix(s*30, 0)), code, "the earliest step is returned")
		}
	})
}

// FuzzCheckPassword: the first broken rule in order (invalid UTF-8 or a control character; too
// short or too long counted in runes after NFC; the username; the common list, both compared
// after NFKC and lower case). A password that passes the length rule is within it after NFC,
// and NFC is stable on it, so the hashed form is the checked form.
func FuzzCheckPassword(f *testing.F) {
	for _, s := range []string{
		"", "short", "fixture pass", "PASSWORD", "password", strings.Repeat("a", 128), strings.Repeat("a", 129),
		strings.Repeat("ﬀ", 64), strings.Repeat("ﬀ", 65), strings.Repeat("́", 8), "fixture\x00pass", "\xff\xfeabcdefgh",
		"Fixture_User", "ＦＩＸＴＵＲＥ＿ＵＳＥＲ", strings.Repeat("ǅ", 8), strings.Repeat("ﷺ", 8), strings.Repeat("é", 70),
	} {
		f.Add(s, "fixture_user")
	}
	f.Fuzz(func(t *testing.T, pw, user string) {
		err := CheckPassword(pw, user)
		if !utf8.ValidString(pw) || strings.ContainsFunc(pw, unicode.IsControl) {
			require.ErrorIs(t, err, ErrPasswordInvalid)
			return
		}
		n := norm.NFC.String(pw)
		switch runes := utf8.RuneCountInString(n); {
		case runes < 8:
			require.ErrorIs(t, err, ErrPasswordTooShort)
			return
		case runes > 128:
			require.ErrorIs(t, err, ErrPasswordTooLong)
			return
		}
		require.Equal(t, n, NormalizePassword(pw), "the hashed form is NFC")
		require.Equal(t, n, NormalizePassword(n), "NFC is stable on a checked password")
		require.False(t, strings.ContainsFunc(n, unicode.IsControl), "NFC adds no control character")
		// Case is ignored by lower-casing (as the common list is stored), not by Unicode folding.
		lower := strings.ToLower(norm.NFKC.String(pw))
		if user != "" && lower == strings.ToLower(norm.NFKC.String(user)) {
			require.ErrorIs(t, err, ErrPasswordIsUsername)
			return
		}
		if err != nil {
			require.ErrorIs(t, err, ErrPasswordCommon)
			_, found := slices.BinarySearch(commonPasswords(), lower)
			require.True(t, found)
		}
	})
}
