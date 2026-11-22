package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRequiresContact(t *testing.T) {
	for name, doc := range map[string]string{
		"empty file":    "",
		"empty contact": "contact: {discord: \"\"}\n",
		"blank contact": "contact: {discord: \"   \"}\n",
	} {
		_, err := Parse([]byte(doc))
		assert.ErrorContains(t, err, "contact.discord is required", name)
	}
}

func TestParseRejectsBadContact(t *testing.T) {
	for _, h := range []string{"rmb938", "@", "@UPPER", "@has space", "@a"} {
		_, err := Parse([]byte("contact: {discord: \"" + h + "\"}\n"))
		assert.ErrorContains(t, err, "must look like", h)
	}
}

func TestParseValidAndDefaults(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\n"))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, cfg.Rate.Interval)
	assert.Equal(t, "./data", cfg.DataDir)
	assert.Equal(t, 2000, cfg.Sources.AutoConfirmMax)
	assert.Equal(t, "personal-ao3-mirror/0.1.0 (personal offline archive; contact: discord @fixture_user)", cfg.UserAgent("0.1.0"))
}

func TestAPIDefaultsAndValidation(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\n"))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8080", cfg.API.Listen)
	assert.Equal(t, "api/admin.sock", cfg.API.AdminSocket)
	assert.Equal(t, []string{"127.0.0.1/32", "::1/128"}, cfg.API.TrustedProxies)
	assert.Equal(t, []string{"127.0.0.1", "::1", "localhost"}, cfg.API.AllowedHosts)
	assert.Equal(t, 24*time.Hour, cfg.API.HashKeyRotation)

	_, err = Parse([]byte("contact: {discord: \"@fixture_user\"}\napi: {hash_key_rotation: 0s}\n"))
	require.NoError(t, err, "rotation 0 means once per process")

	for yml, want := range map[string]string{
		"listen: localhost":              "must be host:port",
		"listen: \":8080\"":              "must be host:port",
		"listen: 127.0.0.1:99999":        "port must be",
		"admin_socket: /tmp/a.sock":      "relative path inside data_dir",
		"admin_socket: ../a.sock":        "relative path inside data_dir",
		"admin_socket: \"\"":             "relative path inside data_dir",
		"trusted_proxies: []":            "must not be empty",
		"trusted_proxies: [10.0.0.1]":    "not a CIDR prefix",
		"allowed_hosts: []":              "allowed_hosts must not be empty",
		"allowed_hosts: [\"a.b:8080\"]":  "not a host name",
		"allowed_hosts: [\"*.ts.net\"]":  "not a host name",
		"allowed_hosts: [\"fe80::1%1\"]": "not a host name",
		"request_timeout: 0s":            "request_timeout",
		"request_timeout: 2m":            "request_timeout",
		"hash_key_rotation: 30m":         "hash_key_rotation",
		"rate: {general_per_min: 0}":     "api.rate",
		"rate: {source_add_per_hour: 0}": "api.rate",
		"rate: {signup_per_hour: 0}":     "api.rate",
		"rate: {login_per_min: 0}":       "api.rate",
		"rate: {refresh_per_min: 0}":     "api.rate",
	} {
		_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\napi: {" + yml + "}\n"))
		assert.ErrorContains(t, err, want, yml)
	}
}

func TestAuthDefaultsAndValidation(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\n"))
	require.NoError(t, err)
	assert.Equal(t, Auth{AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour, MaxSessionAge: 2160 * time.Hour,
		InviteTTL: 24 * time.Hour, TOTPIssuer: "ao3mirror"}, cfg.Auth)

	for _, yml := range []string{
		"access_ttl: 5m", "access_ttl: 1h", "refresh_ttl: 1h", "refresh_ttl: 2160h",
		"refresh_ttl: 2160h, max_session_age: 2160h", "max_session_age: 8760h",
		"invite_ttl: 1m", "invite_ttl: 168h", "totp_issuer: \"My Mirror 2.0_x-y\"", "totp_issuer: a",
		"totp_issuer: " + strings.Repeat("a", 32),
	} {
		_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nauth: {" + yml + "}\n"))
		require.NoError(t, err, yml)
	}
	for yml, want := range map[string]string{
		"access_ttl: 4m":                          "auth.access_ttl",
		"access_ttl: 61m":                         "auth.access_ttl",
		"refresh_ttl: 59m":                        "auth.refresh_ttl",
		"refresh_ttl: 2161h":                      "auth.refresh_ttl",
		"max_session_age: 719h":                   "auth.max_session_age",
		"max_session_age: 8761h":                  "auth.max_session_age",
		"invite_ttl: 59s":                         "auth.invite_ttl",
		"invite_ttl: 169h":                        "auth.invite_ttl",
		"totp_issuer: \"\"":                       "auth.totp_issuer",
		"totp_issuer: \" lead\"":                  "auth.totp_issuer",
		"totp_issuer: \"trail \"":                 "auth.totp_issuer",
		"totp_issuer: \"a:b\"":                    "auth.totp_issuer",
		"totp_issuer: \"é\"":                      "auth.totp_issuer",
		"totp_issuer: " + strings.Repeat("a", 33): "auth.totp_issuer",
		"enabled: false":                          "field enabled not found",
	} {
		_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nauth: {" + yml + "}\n"))
		assert.ErrorContains(t, err, want, yml)
	}
}

func TestPublicURL(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\n"))
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080", cfg.API.PublicURL)

	const hosts = `allowed_hosts: ["127.0.0.1", "::1", "localhost", "mirror.example.com"]`
	for _, u := range []string{
		"https://mirror.example.com", "https://mirror.example.com/", "http://mirror.example.com:8443",
		"http://127.0.0.1", "http://[::1]:8080",
	} {
		_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\napi: {" + hosts + ", public_url: \"" + u + "\"}\n"))
		require.NoError(t, err, u)
	}
	for u, want := range map[string]string{
		"":                                 "http(s) origin",
		"mirror.example.com":               "http(s) origin",
		"ftp://mirror.example.com":         "http(s) origin",
		"https://":                         "http(s) origin",
		"https://u:p@mirror.example.com":   "http(s) origin",
		"https://mirror.example.com/api":   "http(s) origin",
		"https://mirror.example.com?":      "http(s) origin",
		"https://mirror.example.com?a=1":   "http(s) origin",
		"https://mirror.example.com#":      "http(s) origin",
		"https://mirror.example.com#x":     "http(s) origin",
		"https://Mirror.example.com":       "lower-case",
		"https://bad_host.example.com":     "lower-case host name",
		"https://mirror.example.com:0":     "port must be",
		"https://mirror.example.com:99999": "port must be",
		"https://mirror.example.com:":      "empty port",
		"https://other.example.com":        "must be in api.allowed_hosts",
		"http://[::2]":                     "must be in api.allowed_hosts",
	} {
		_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\napi: {" + hosts + ", public_url: \"" + u + "\"}\n"))
		assert.ErrorContains(t, err, want, u)
	}
}

func TestUserAgentSuffixAppendsOnly(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nuser_agent_suffix: extra\n"))
	require.NoError(t, err)
	ua := cfg.UserAgent("dev")
	assert.Contains(t, ua, "contact: discord @fixture_user")
	assert.True(t, strings.HasSuffix(ua, " extra"), "UserAgent = %q", ua)
}

func TestIntervalFloor(t *testing.T) {
	_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nrate: {interval: 1s}\n"))
	assert.ErrorContains(t, err, "below the hard floor")
}

func TestUnknownKeyRejected(t *testing.T) {
	_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nrate: {intervl: 10s}\n"))
	assert.Error(t, err, "want unknown key error")
}

func TestExampleConfigFailsUntilContactSet(t *testing.T) {
	raw, err := os.ReadFile("../../ao3mirror.example.yaml")
	require.NoError(t, err)
	_, err = Parse(raw)
	require.ErrorContains(t, err, "contact.discord is required", "example config should fail on contact")
	fixed := strings.Replace(string(raw), `discord: ""`, `discord: "@fixture_user"`, 1)
	_, err = Parse([]byte(fixed))
	require.NoError(t, err, "example config with contact should be valid")
}

func TestValidHost(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "::1", "mirror.tail1234.ts.net", "A-1.example"} {
		assert.True(t, ValidHost(h), h)
	}
	for _, h := range []string{
		"", "a.b:8080", "[::1]", "*.ts.net", "fe80::1%eth0", "a..b", "a.", "-a.b", "a-.b", "a_b",
		strings.Repeat("a", 64) + ".b", strings.Repeat("a.", 127) + "aa",
	} {
		assert.False(t, ValidHost(h), h)
	}
}
