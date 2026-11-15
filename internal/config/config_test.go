package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseRequiresContact(t *testing.T) {
	for name, doc := range map[string]string{
		"empty file":    "",
		"empty contact": "contact: {discord: \"\"}\n",
		"blank contact": "contact: {discord: \"   \"}\n",
	} {
		_, err := Parse([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), "contact.discord is required") {
			t.Errorf("%s: want contact required error, got %v", name, err)
		}
	}
}

func TestParseRejectsBadContact(t *testing.T) {
	for _, h := range []string{"rmb938", "@", "@UPPER", "@has space", "@a"} {
		_, err := Parse([]byte("contact: {discord: \"" + h + "\"}\n"))
		if err == nil || !strings.Contains(err.Error(), "must look like") {
			t.Errorf("%q: want format error, got %v", h, err)
		}
	}
}

func TestParseValidAndDefaults(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rate.Interval != 10*time.Second || cfg.DataDir != "./data" || cfg.Sources.AutoConfirmMax != 2000 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	ua := cfg.UserAgent("0.1.0")
	want := "personal-ao3-mirror/0.1.0 (personal offline archive; contact: discord @fixture_user)"
	if ua != want {
		t.Errorf("UserAgent = %q, want %q", ua, want)
	}
}

func TestUserAgentSuffixAppendsOnly(t *testing.T) {
	cfg, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nuser_agent_suffix: extra\n"))
	if err != nil {
		t.Fatal(err)
	}
	ua := cfg.UserAgent("dev")
	if !strings.Contains(ua, "contact: discord @fixture_user") || !strings.HasSuffix(ua, " extra") {
		t.Errorf("UserAgent = %q", ua)
	}
}

func TestIntervalFloor(t *testing.T) {
	_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nrate: {interval: 1s}\n"))
	if err == nil || !strings.Contains(err.Error(), "below the hard floor") {
		t.Errorf("want floor error, got %v", err)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	_, err := Parse([]byte("contact: {discord: \"@fixture_user\"}\nrate: {intervl: 10s}\n"))
	if err == nil {
		t.Error("want unknown key error")
	}
}

func TestExampleConfigFailsUntilContactSet(t *testing.T) {
	raw, err := os.ReadFile("../../ao3mirror.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "contact.discord is required") {
		t.Fatalf("example config should fail on contact, got %v", err)
	}
	fixed := strings.Replace(string(raw), `discord: ""`, `discord: "@fixture_user"`, 1)
	if _, err := Parse([]byte(fixed)); err != nil {
		t.Fatalf("example config with contact should be valid: %v", err)
	}
}
