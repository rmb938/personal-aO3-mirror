package runtime

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/redact"
)

// The stage 17a fuzz targets: the Authorization header and the dto Verbatim patterns, each
// checked against an oracle written from the documented rules.

// bearerRE is RFC 6750 §2.1 credentials: the scheme (any case), one or more spaces, then one
// token68 (RFC 9110 §11.2).
var (
	bearerRE       = regexp.MustCompile(`\A[Bb][Ee][Aa][Rr][Ee][Rr] +([A-Za-z0-9._~+/-]+=*)\z`)
	bearerSchemeRE = regexp.MustCompile(`\A[Bb][Ee][Aa][Rr][Ee][Rr]\z`)
)

// FuzzBearerToken: one or two Authorization values give a token only for exactly one value of
// at most MaxAuthorizationBytes that is "Bearer" (any ASCII case), spaces and one token68.
// Another scheme (the text before the first space isn't "bearer") is missing credentials;
// anything else is malformed.
func FuzzBearerToken(f *testing.F) {
	const jwt = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIxIn0.c2ln"
	for _, s := range []string{
		"", "Bearer", "Bearer ", "Bearer " + jwt, "bEaReR  " + jwt, "Bearer\t" + jwt, "Bearer " + jwt + " ",
		" Bearer " + jwt, "Bearer a=b", "Bearer ==", "Bearer a==", "Basic Zml4dHVyZQ==", "Bearer é", "Bearer a\x00",
		"BEARER " + jwt, "Bearer " + strings.Repeat("a", MaxAuthorizationBytes-7),
		"Bearer " + strings.Repeat("a", MaxAuthorizationBytes-6), "Bearerx " + jwt, "Bearer a b",
	} {
		f.Add(s, "", false)
		f.Add(s, "Bearer "+jwt, true)
	}
	f.Fuzz(func(t *testing.T, a, b string, two bool) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header["Authorization"] = []string{a}
		if two {
			r.Header["Authorization"] = append(r.Header["Authorization"], b)
		}
		token, _, res := bearerToken(r)

		wantRes, wantToken := authMalformed, ""
		scheme, _, _ := strings.Cut(a, " ")
		switch {
		case two || len(a) > MaxAuthorizationBytes:
		case !bearerSchemeRE.MatchString(scheme):
			wantRes = authMissing
		default:
			if m := bearerRE.FindStringSubmatch(a); m != nil {
				wantRes, wantToken = authOK, m[1]
			}
		}
		require.Equal(t, wantRes, res, "%q (two %v)", a, two)
		require.Equal(t, wantToken, token)
	})
}

// FuzzVerbatimPatterns sends any string as each dto Verbatim field (verbatimBody) through the
// real enforcer in both modes. It passes unchanged exactly when it fully matches the type's
// VerbatimPattern, else the enforcer refuses with a *redact.PatternError (a 500). A value
// that passes is also what internal/auth makes or accepts for that field.
func FuzzVerbatimPatterns(f *testing.F) {
	saved := responseTypes // no runtime test runs in parallel
	responseTypes = append(slices.Clip(responseTypes), verbatimBody{})
	h, err := redact.NewHasher(0, nil)
	require.NoError(f, err)
	e, err := NewEnforcer(h, "v0.0.0-fuzz")
	responseTypes = saved
	require.NoError(f, err)

	typ := reflect.TypeFor[verbatimBody]()
	type field struct {
		key string
		re  *regexp.Regexp
	}
	var fields []field
	for sf := range typ.Fields() {
		ft := sf.Type
		if ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		pat := reflect.Zero(ft).Interface().(redact.Verbatim).VerbatimPattern()
		key, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		fields = append(fields, field{key, regexp.MustCompile(`\A(?:` + pat + `)\z`)})
	}

	vals := verbatimValues(f)
	for k, fl := range fields {
		for _, v := range vals[fl.key][:2] {
			for _, s := range []string{v, v + "\n", strings.ToUpper(v), v[:len(v)-1], " " + v, v + sentinel} {
				f.Add(uint8(k), s)
			}
		}
		f.Add(uint8(k), sentinel)
	}
	f.Fuzz(func(t *testing.T, which uint8, s string) {
		fl := fields[int(which)%len(fields)]
		if s == "" {
			return // omitted (omitzero)
		}
		raw, err := json.Marshal(s) // replaces invalid UTF-8 with U+FFFD
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &s))
		if fl.key == "recovery_codes" {
			raw = []byte("[" + string(raw) + "]")
		}
		in := []byte(`{"` + fl.key + `":` + string(raw) + `}`)

		match := fl.re.MatchString(s)
		for _, mode := range []redact.Mode{redact.Hashed, redact.Revealed} {
			res, err := e.Enforce(mode, typ, in)
			if !match {
				_, ok := errors.AsType[*redact.PatternError](err)
				require.True(t, ok, "%s %q: want *PatternError, got %v", fl.key, s, err)
				continue
			}
			require.NoError(t, err, "%s %q", fl.key, s)
			require.JSONEq(t, string(in), string(res.Body), "passes unchanged")
		}
		if match {
			requireAuthShaped(t, fl.key, s)
		}
	})
}

var (
	totpB32Fuzz = base32.StdEncoding.WithPadding(base32.NoPadding)
	usernameRE  = regexp.MustCompile(`\A[a-z0-9_-]{3,32}\z`)
)

// requireAuthShaped checks that a value passing a Verbatim pattern is one internal/auth would
// make for that field: the pattern is no looser than the format.
func requireAuthShaped(t *testing.T, key, s string) {
	t.Helper()
	opaque := func(k auth.TokenKind, tok string) {
		_, ok := auth.TokenSum(k, tok)
		require.True(t, ok, "%s %q is a %s token", key, tok, k)
	}
	switch key {
	case "username":
		require.Regexp(t, usernameRE, s)
	case "access_token":
		parts := strings.Split(s, ".")
		require.Len(t, parts, 3)
		// The header is the fixed form around the kid's 18 bytes (its quotes and 16
		// characters). The pattern can't pin those bytes to `"…"` and base64url without a far
		// longer regex, and the claims are free bytes anyway (an accepted gap;
		// docs/part2-api.md 17a).
		hdr, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
		require.NoError(t, err)
		kid, ok := strings.CutPrefix(string(hdr), `{"alg":"EdDSA","kid":`)
		require.True(t, ok, "%q", hdr)
		kid, ok = strings.CutSuffix(kid, `,"typ":"JWT"}`)
		require.True(t, ok, "%q", hdr)
		require.Len(t, kid, 18)
		sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
		require.NoError(t, err)
		require.Len(t, sig, 64)
	case "enrollment_token":
		opaque(auth.EnrollmentToken, s)
	case "challenge_token":
		opaque(auth.ChallengeToken, s)
	case "refresh_token":
		opaque(auth.RefreshToken, s)
	case "totp_secret":
		b, err := totpB32Fuzz.DecodeString(s)
		require.NoError(t, err)
		require.Len(t, b, auth.TOTPSecretBytes)
	case "otpauth_uri":
		u, err := url.Parse(s)
		require.NoError(t, err)
		require.Equal(t, "otpauth", u.Scheme)
		require.Equal(t, "totp", u.Host)
		b, err := totpB32Fuzz.DecodeString(u.Query().Get("secret"))
		require.NoError(t, err)
		require.Len(t, b, auth.TOTPSecretBytes)
	case "recovery_codes":
		n, ok := auth.NormalizeRecoveryCode(s)
		require.True(t, ok)
		require.Equal(t, strings.ReplaceAll(s, "-", ""), n)
	case "invite_url":
		u, err := url.Parse(s)
		require.NoError(t, err)
		require.Contains(t, []string{"http", "https"}, u.Scheme)
		require.NotEmpty(t, u.Host)
		tok, ok := strings.CutPrefix(u.Fragment, "token=")
		require.True(t, ok)
		opaque(auth.InviteToken, tok)
	default:
		t.Fatalf("requireAuthShaped: no format for %s; add it", key)
	}
}
