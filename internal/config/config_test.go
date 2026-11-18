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
		"request_timeout: 0s":            "request_timeout",
		"request_timeout: 2m":            "request_timeout",
		"hash_key_rotation: 30m":         "hash_key_rotation",
		"rate: {general_per_min: 0}":     "api.rate",
		"rate: {source_add_per_hour: 0}": "api.rate",
	} {
		_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\napi: {" + yml + "}\n"))
		assert.ErrorContains(t, err, want, yml)
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
