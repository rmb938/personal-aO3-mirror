package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rfcSecret is the HMAC-SHA1 key of RFC 4226 Appendix D and RFC 6238 Appendix B.
var rfcSecret = []byte("12345678901234567890")

func TestHOTPRFC4226(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		require.Equal(t, w, hotp(rfcSecret, uint64(i), 6), "counter %d", i)
	}
}

func TestTOTPRFC6238(t *testing.T) {
	// Appendix B, the SHA1 rows (8 digits).
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		step := TOTPStep(time.Unix(tc.unix, 0))
		require.Equal(t, tc.code, hotp(rfcSecret, uint64(step), 8), "T=%d", tc.unix) //nolint:gosec // G115: a positive step
	}
}

func TestVerifyTOTP(t *testing.T) {
	secret := NewTOTPSecret()
	require.Len(t, secret, TOTPSecretBytes)
	now := time.Date(2026, 11, 21, 12, 0, 10, 0, time.UTC)
	cur := TOTPStep(now)
	code := func(step int64) string { return hotp(secret, uint64(step), TOTPDigits) } //nolint:gosec // G115: a positive step

	step, ok := VerifyTOTP(secret, TOTPCode(secret, now), now, 0)
	require.True(t, ok)
	require.Equal(t, cur, step)

	for _, d := range []int64{-1, 1} {
		step, ok := VerifyTOTP(secret, code(cur+d), now, 0)
		require.True(t, ok, "skew %d", d)
		require.Equal(t, cur+d, step)
	}
	for _, d := range []int64{-2, 2} {
		_, ok := VerifyTOTP(secret, code(cur+d), now, 0)
		require.False(t, ok, "skew %d", d)
	}

	// Replay: the step just used, or an earlier one, is refused.
	_, ok = VerifyTOTP(secret, code(cur), now, cur)
	require.False(t, ok)
	_, ok = VerifyTOTP(secret, code(cur-1), now, cur)
	require.False(t, ok)
	step, ok = VerifyTOTP(secret, code(cur+1), now, cur)
	require.True(t, ok)
	require.Equal(t, cur+1, step)

	// Another secret's code fails.
	_, ok = VerifyTOTP(NewTOTPSecret(), code(cur), now, 0)
	require.False(t, ok)

	for _, bad := range []string{"", "12345", "1234567", "12345a", " 123456", "123456 ", "١٢٣٤٥٦", "+12345"} {
		_, ok := VerifyTOTP(secret, bad, now, 0)
		require.False(t, ok, "%q", bad)
	}
}

func TestTOTPURI(t *testing.T) {
	secret := []byte("12345678901234567890")
	require.Equal(t, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", EncodeTOTPSecret(secret))
	require.Equal(t,
		"otpauth://totp/ao3%20mirror:fixture_user?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"+
			"&issuer=ao3%20mirror&algorithm=SHA1&digits=6&period=30",
		TOTPURI("ao3 mirror", "fixture_user", secret))
}
