package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// verbatimBody holds one field of each dto Verbatim type. No route returns them yet (stage 16e
// adds the first), so these tests register it themselves.
type verbatimBody struct {
	Username      dto.Username        `json:"username,omitzero"`
	AccessToken   dto.AccessToken     `json:"access_token,omitzero"`
	Enrollment    dto.EnrollmentToken `json:"enrollment_token,omitzero"`
	Challenge     dto.ChallengeToken  `json:"challenge_token,omitzero"`
	Refresh       dto.RefreshToken    `json:"refresh_token,omitzero"`
	TOTPSecret    dto.TOTPSecret      `json:"totp_secret,omitzero"`
	OTPAuthURI    dto.OTPAuthURI      `json:"otpauth_uri,omitzero"`
	RecoveryCodes []dto.RecoveryCode  `json:"recovery_codes,omitzero"`
	InviteURL     dto.InviteURL       `json:"invite_url,omitzero"`
}

// verbatimAPI returns an API whose enforcer knows verbatimBody, and a func that serves one
// field (json key, value; recovery_codes takes one code) at /v and returns the response.
func verbatimAPI(t *testing.T) (serve func(target, key, value string) (code int, body string), logs *bytes.Buffer) {
	t.Helper()
	saved := responseTypes // no runtime test runs in parallel
	responseTypes = append(slices.Clip(responseTypes), verbatimBody{})
	t.Cleanup(func() { responseTypes = saved })

	a, logBuf := testutil.NewAPI(t, testOptions, testNew, nil)
	var cur verbatimBody
	a.Router(false).Get("/v", a.Handle(func(*http.Request) (any, error) { return cur, nil }))
	serve = func(target, key, value string) (int, string) {
		t.Helper()
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		if key == "recovery_codes" {
			raw = []byte("[" + string(raw) + "]")
		}
		cur = verbatimBody{}
		require.NoError(t, json.Unmarshal([]byte(`{"`+key+`":`+string(raw)+`}`), &cur))
		w := testutil.Get(t, a.Router(false), target)
		return w.Code, w.Body.String()
	}
	return serve, logBuf
}

// verbatimValues are values for each field as internal/auth generates them, by json key.
func verbatimValues(t *testing.T) map[string][]string {
	t.Helper()
	// Several of each random value, so the bit-limited last characters vary.
	const n = 32
	many := func(f func(i int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	token := func(k auth.TokenKind) func(int) string {
		return func(int) string { s, _ := auth.NewToken(k); return s }
	}
	jwt := func(i int) string {
		s, _, err := auth.NewIssuer("https://mirror.example.com", auth.NewSigningKey(), 15*time.Minute, time.Now).
			Issue(int64(i+1), int64(i+1)*1_000_000_007)
		require.NoError(t, err)
		return s
	}
	hosts := []string{"https://mirror.example.com/signup", "http://127.0.0.1:8080/reset", "http://[::1]:8080/signup"}
	issuers := []string{"ao3mirror", "My Mirror 2.0_x-y"}
	return map[string][]string{
		"username":         {"fixture_user", "abc", strings.Repeat("z", 32), "a-b_9"},
		"access_token":     many(jwt),
		"enrollment_token": many(token(auth.EnrollmentToken)),
		"challenge_token":  many(token(auth.ChallengeToken)),
		"refresh_token":    many(token(auth.RefreshToken)),
		"totp_secret":      many(func(int) string { return auth.EncodeTOTPSecret(auth.NewTOTPSecret()) }),
		"otpauth_uri": many(func(i int) string {
			return auth.TOTPURI(issuers[i%len(issuers)], "fixture_user", auth.NewTOTPSecret())
		}),
		"recovery_codes": auth.NewRecoveryCodes(),
		"invite_url": many(func(i int) string {
			return hosts[i%len(hosts)] + "#token=" + token(auth.InviteToken)(i)
		}),
	}
}

// Values internal/auth generates pass every Verbatim pattern unhashed, in both modes.
func TestVerbatimPassesAuthValues(t *testing.T) {
	serve, _ := verbatimAPI(t)
	for key, vals := range verbatimValues(t) {
		for _, v := range vals {
			for _, target := range []string{"/v", "/v" + revealQuery} {
				code, body := serve(target, key, v)
				require.Equal(t, http.StatusOK, code, "%s %s", target, key)
				var got map[string]any
				require.NoError(t, json.Unmarshal([]byte(body), &got))
				want := any(v)
				if key == "recovery_codes" {
					want = []any{v}
				}
				assert.Equal(t, want, got[key], "%s %s passes unchanged", target, key)
			}
		}
	}
}

// A Verbatim field never passes free text or a near miss: the response is a 500 that holds no
// trace of the value, in both modes.
func TestVerbatimMismatchIs500(t *testing.T) {
	serve, logs := verbatimAPI(t)
	for key, vals := range verbatimValues(t) {
		good := vals[0]
		for _, v := range []string{
			sentinel + "x", sentinel + "fixture title one, lorem ipsum dolor", " ", // "" is omitted (omitzero)
			good + " " + sentinel, good + "\n", " " + good, good + sentinel, sentinel + good,
			strings.ToUpper(good) + "_", good[:len(good)-1] + "é", good + "/" + sentinel,
		} {
			for _, target := range []string{"/v", "/v" + revealQuery} {
				code, body := serve(target, key, v)
				require.Equal(t, http.StatusInternalServerError, code, "%s %s %q", target, key, v)
				assert.Contains(t, body, string(dto.TypeInternal))
				assert.NotContains(t, body, sentinel)
				if v != " " {
					assert.NotContains(t, body, strings.TrimSpace(v))
				}
			}
		}
	}
	assert.Contains(t, logs.String(), "does not match the pattern of runtime.verbatimBody.")
	assert.NotContains(t, logs.String(), sentinel)
}
