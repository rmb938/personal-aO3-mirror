package auth

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 TOTP with SHA-1 is what authenticator apps support; HMAC-SHA1 is not broken
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"net/url"
	"strconv"
	"time"
)

// TOTP parameters (RFC 6238 defaults, which every authenticator app supports).
const (
	TOTPDigits      = 6
	TOTPPeriod      = 30 * time.Second
	TOTPSkew        = 1  // steps accepted either side of now
	TOTPSecretBytes = 20 // 160 bits, RFC 4226 §4's recommendation
)

// totpB32 is the secret encoding authenticator apps take (RFC 4648 base32, no padding).
var totpB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random TOTPSecretBytes secret.
func NewTOTPSecret() []byte { return randomBytes(TOTPSecretBytes) }

// EncodeTOTPSecret is the base32 form of secret that a user types into an authenticator app.
func EncodeTOTPSecret(secret []byte) string { return totpB32.EncodeToString(secret) }

// TOTPStep is the time step now falls in (RFC 6238 T, with T0 = 0).
func TOTPStep(now time.Time) int64 { return now.Unix() / int64(TOTPPeriod/time.Second) }

// VerifyTOTP checks a TOTPDigits code against the steps within TOTPSkew of now. A step at or
// before lastStep is never accepted, so a code can't be used twice (RFC 6238 §5.2); store the
// returned step as the user's new lastStep. A user who has never logged in has lastStep 0. The
// code is compared in constant time against every candidate step.
func VerifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	if len(code) != TOTPDigits || !allDigits(code) {
		return 0, false
	}
	cur := TOTPStep(now)
	for s := cur - TOTPSkew; s <= cur+TOTPSkew; s++ {
		match := subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(s), TOTPDigits)), []byte(code)) == 1 //nolint:gosec // G115: s is a positive Unix step
		if match && s > lastStep && !ok {
			step, ok = s, true
		}
	}
	return step, ok
}

// TOTPCode is the code for now, for tests and for nothing else in production.
func TOTPCode(secret []byte, now time.Time) string {
	return hotp(secret, uint64(TOTPStep(now)), TOTPDigits) //nolint:gosec // G115: a positive Unix step
}

// hotp is RFC 4226 §5.3 HOTP with HMAC-SHA1 and dynamic truncation, zero-padded to digits.
func hotp(secret []byte, counter uint64, digits int) string {
	mac := hmac.New(sha1.New, secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	s := strconv.FormatUint(uint64(bin%mod), 10)
	for len(s) < digits {
		s = "0" + s
	}
	return s
}

// TOTPURI is the otpauth:// URI an authenticator app scans (Google Authenticator's Key URI
// Format): label "issuer:account", then secret, issuer, algorithm, digits and period. Spaces
// are %20, never '+', which some apps show literally.
func TOTPURI(issuer, account string, secret []byte) string {
	return "otpauth://totp/" + url.PathEscape(issuer) + ":" + url.PathEscape(account) +
		"?secret=" + EncodeTOTPSecret(secret) +
		"&issuer=" + url.PathEscape(issuer) +
		"&algorithm=SHA1&digits=" + strconv.Itoa(TOTPDigits) +
		"&period=" + strconv.Itoa(int(TOTPPeriod/time.Second))
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
