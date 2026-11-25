// Package config loads and validates ao3mirror.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/redact"
)

// MinInterval is the hard floor between AO3 requests. Config cannot go below it.
const MinInterval = 3 * time.Second

// discordHandle matches Discord usernames (2-32 chars: a-z, 0-9, '_' and '.') with a leading '@'.
var discordHandle = regexp.MustCompile(`^@[a-z0-9_.]{2,32}$`)

type Config struct {
	DataDir         string  `yaml:"data_dir"`
	Contact         Contact `yaml:"contact"`
	UserAgentSuffix string  `yaml:"user_agent_suffix"`
	Log             Log     `yaml:"log"`
	Rate            Rate    `yaml:"rate"`
	Sources         Sources `yaml:"sources"`
	Tags            Tags    `yaml:"tags"`
	Series          Series  `yaml:"series"`
	Images          Images  `yaml:"images"`
	Content         Content `yaml:"content"`
	Auth            Auth    `yaml:"auth"`
	API             API     `yaml:"api"`
}

// API configures `ao3mirror serve` (docs/part2-api.md, "Config").
type API struct {
	// Listen is the public TCP listener (host:port); the Cloudflare Tunnel points here.
	Listen string `yaml:"listen"`
	// AdminSocket is the admin unix socket, relative to data_dir.
	AdminSocket string `yaml:"admin_socket"`
	// TrustedProxies are the CIDR prefixes whose X-Forwarded-For is trusted.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// AllowedHosts are the host names (no port) the public listener answers to; any other Host
	// header gets 421, which stops DNS rebinding. Add the tunnel's custom domain when enabling it.
	AllowedHosts []string `yaml:"allowed_hosts"`
	// RequestTimeout is the per-request context deadline.
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// HashKeyRotation is the in-memory hash key lifetime; 0 means one key per process.
	HashKeyRotation time.Duration `yaml:"hash_key_rotation"`
	// PublicURL is the origin users reach the API at (scheme, lower-case host, optional port;
	// no path): the access tokens' issuer and the base of invite links. Its host must be in
	// AllowedHosts.
	PublicURL string  `yaml:"public_url"`
	Rate      APIRate `yaml:"rate"`
}

// APIRate holds the per-client rate limits of the public listener (the admin socket's are
// higher). SearchPerMin is shared by the QUERY search routes, SignupPerHour by the signup and
// reset routes (both steps). LoginPerMin applies to each login step separately. UIPerMin is
// the web UI's static files, a budget of their own: one page load fetches many of them.
type APIRate struct {
	GeneralPerMin    int `yaml:"general_per_min"`
	UIPerMin         int `yaml:"ui_per_min"`
	SearchPerMin     int `yaml:"search_per_min"`
	SourceAddPerHour int `yaml:"source_add_per_hour"`
	SignupPerHour    int `yaml:"signup_per_hour"`
	LoginPerMin      int `yaml:"login_per_min"`
	RefreshPerMin    int `yaml:"refresh_per_min"`
}

// MaxRequestTimeout caps api.request_timeout below the server's 60s write timeout, so a slow
// handler gets its 503 out before the connection is cut.
const MaxRequestTimeout = 50 * time.Second

type Contact struct {
	Discord string `yaml:"discord"`
}

type Log struct {
	Level     string `yaml:"level"`
	File      string `yaml:"file"`
	MaxSizeMB int    `yaml:"max_size_mb"`
	MaxFiles  int    `yaml:"max_files"`
}

type Rate struct {
	Interval    time.Duration `yaml:"interval"`
	Jitter      float64       `yaml:"jitter"`
	DailyBudget int           `yaml:"daily_budget"`
}

type Sources struct {
	AutoConfirmMax       int           `yaml:"auto_confirm_max"`
	DefaultBackfillOrder string        `yaml:"default_backfill_order"`
	PollInterval         time.Duration `yaml:"poll_interval"`
	FullRescanInterval   time.Duration `yaml:"full_rescan_interval"`
}

type Tags struct {
	InfoTTL time.Duration `yaml:"info_ttl"`
	// InfoTypes are the tag types whose tag pages are fetched for wrangling info (canonical,
	// synonyms, parents). ~1 request per unique tag; empty disables tag-info fetching.
	InfoTypes []string `yaml:"info_types"`
}

// Series controls fetching series pages for series that are not sources themselves.
type Series struct {
	Info    bool          `yaml:"info"`     // fetch description/stats of series that mirrored works belong to
	InfoTTL time.Duration `yaml:"info_ttl"` // refresh at least this often (sooner when a member work is revised)
}

// TagInfoTypes are the valid values for tags.info_types.
var TagInfoTypes = map[string]bool{"Fandom": true, "Relationship": true, "Character": true, "Freeform": true}

type Images struct {
	Enabled         bool          `yaml:"enabled"`
	PerHostInterval time.Duration `yaml:"per_host_interval"`
	MaxBytes        int64         `yaml:"max_bytes"`
}

type Content struct {
	KeepVersions int `yaml:"keep_versions"`
}

// Auth configures the API's user accounts (docs/part2-api.md stage 16). Auth can't be turned
// off: every public route but healthz and the auth entry points needs a token.
type Auth struct {
	AccessTTL  time.Duration `yaml:"access_ttl"`  // access token lifetime
	RefreshTTL time.Duration `yaml:"refresh_ttl"` // idle limit: each refresh extends the session by it
	// MaxSessionAge ends a session this long after its login however often it is refreshed.
	MaxSessionAge time.Duration `yaml:"max_session_age"`
	InviteTTL     time.Duration `yaml:"invite_ttl"`  // default lifetime of a new invite or reset link
	TOTPIssuer    string        `yaml:"totp_issuer"` // the issuer authenticator apps show
	// SigningKeyRotation is how old the access-token signing key gets before serve replaces it.
	SigningKeyRotation time.Duration `yaml:"signing_key_rotation"`
	// MasterKeyRotation is how old the master key (keys.db) gets before serve replaces it and
	// re-encrypts users.db (docs/part3-key-rotation.md).
	MasterKeyRotation time.Duration `yaml:"master_key_rotation"`
}

// Auth bounds (docs/part2-api.md stage 16 "Config"). MinInviteTTL and MaxInviteTTL match
// controllers.MinInviteTTL and MaxInviteTTL.
const (
	MinAccessTTL     = 5 * time.Minute
	MaxAccessTTL     = time.Hour
	MinRefreshTTL    = time.Hour
	MaxRefreshTTL    = 2160 * time.Hour
	MaxMaxSessionAge = 8760 * time.Hour
	MinInviteTTL     = time.Minute
	MaxInviteTTL     = 168 * time.Hour
	// NIST SP 800-57 Part 1 Rev. 5 caps a signature key's use at about 1-3 years; rotating is
	// cheap here, so the default is far shorter (docs/part2-api.md stage 16h).
	MinSigningKeyRotation = 24 * time.Hour
	MaxSigningKeyRotation = 8760 * time.Hour
	// The master key encrypts; rotating it is cheap too (a few rows re-encrypted), so it uses
	// the same bounds (docs/part3-key-rotation.md).
	MinMasterKeyRotation = 24 * time.Hour
	MaxMasterKeyRotation = 8760 * time.Hour
)

// totpIssuer is auth.totp_issuer: 1-32 of letters, digits, space, '.', '_' and '-', not
// starting or ending with a space.
var totpIssuer = regexp.MustCompile(`^[A-Za-z0-9._-]([A-Za-z0-9 ._-]{0,30}[A-Za-z0-9._-])?$`)

// Default returns the default config. Contact.Discord is intentionally empty: it is required.
func Default() Config {
	return Config{
		DataDir: "./data",
		Log:     Log{Level: "info", File: "logs/ao3mirror.log", MaxSizeMB: 50, MaxFiles: 5},
		Rate:    Rate{Interval: 10 * time.Second, Jitter: 0.2},
		Sources: Sources{
			AutoConfirmMax:       2000,
			DefaultBackfillOrder: "kudos_desc",
			PollInterval:         6 * time.Hour,
			FullRescanInterval:   30 * 24 * time.Hour,
		},
		Tags:    Tags{InfoTTL: 30 * 24 * time.Hour, InfoTypes: []string{"Fandom", "Relationship", "Character"}},
		Series:  Series{Info: true, InfoTTL: 30 * 24 * time.Hour},
		Images:  Images{Enabled: true, PerHostInterval: 3 * time.Second, MaxBytes: 25 << 20},
		Content: Content{KeepVersions: 1},
		Auth: Auth{
			AccessTTL:     15 * time.Minute,
			RefreshTTL:    720 * time.Hour,
			MaxSessionAge: 720 * time.Hour, // NIST SP 800-63B-4 §2.1.3: AAL1's SHOULD of ≤ 30 days
			InviteTTL:     24 * time.Hour,
			TOTPIssuer:    "ao3mirror",

			SigningKeyRotation: 720 * time.Hour,
			MasterKeyRotation:  720 * time.Hour,
		},
		API: API{
			Listen:          "127.0.0.1:8080",
			AdminSocket:     "api/admin.sock",
			TrustedProxies:  []string{"127.0.0.1/32", "::1/128"},
			AllowedHosts:    []string{"127.0.0.1", "::1", "localhost"},
			RequestTimeout:  10 * time.Second,
			HashKeyRotation: 24 * time.Hour,
			PublicURL:       "http://localhost:8080",
			Rate: APIRate{GeneralPerMin: 120, UIPerMin: 600, SearchPerMin: 30, SourceAddPerHour: 10,
				SignupPerHour: 5, LoginPerMin: 10, RefreshPerMin: 30},
		},
	}
}

// Load reads path, applies it over Default(), and validates the result.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw)
}

// Parse decodes YAML over Default() and validates. Unknown keys are errors.
func Parse(raw []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks every field. All problems are reported together.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if strings.TrimSpace(c.Contact.Discord) == "" {
		add("contact.discord is required (e.g. \"@yourname\"); it is sent in the User-Agent so AO3 can contact you")
	} else if !discordHandle.MatchString(c.Contact.Discord) {
		add("contact.discord %q must look like \"@name\" (2-32 chars: a-z, 0-9, '_' or '.')", c.Contact.Discord)
	}
	if strings.ContainsAny(c.UserAgentSuffix, "\r\n") {
		add("user_agent_suffix must not contain newlines")
	}
	if c.DataDir == "" {
		add("data_dir is required")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		add("log.level %q must be debug|info|warn|error", c.Log.Level)
	}
	if c.Log.MaxSizeMB < 1 || c.Log.MaxFiles < 1 {
		add("log.max_size_mb and log.max_files must be >= 1")
	}
	if c.Rate.Interval < MinInterval {
		add("rate.interval %s is below the hard floor %s", c.Rate.Interval, MinInterval)
	}
	if c.Rate.Jitter < 0 || c.Rate.Jitter > 0.5 {
		add("rate.jitter %v must be between 0 and 0.5", c.Rate.Jitter)
	}
	if c.Rate.DailyBudget < 0 {
		add("rate.daily_budget must be >= 0")
	}
	if c.Sources.AutoConfirmMax < 0 {
		add("sources.auto_confirm_max must be >= 0")
	}
	if _, err := ao3url.ParseOrder(c.Sources.DefaultBackfillOrder); err != nil {
		add("sources.default_backfill_order: %v", err)
	}
	if c.Sources.PollInterval < 10*time.Minute {
		add("sources.poll_interval %s must be >= 10m", c.Sources.PollInterval)
	}
	if c.Sources.FullRescanInterval < 24*time.Hour {
		add("sources.full_rescan_interval %s must be >= 24h", c.Sources.FullRescanInterval)
	}
	if c.Tags.InfoTTL < time.Hour {
		add("tags.info_ttl must be >= 1h")
	}
	if c.Series.InfoTTL < time.Hour {
		add("series.info_ttl must be >= 1h")
	}
	for _, t := range c.Tags.InfoTypes {
		if !TagInfoTypes[t] {
			add("tags.info_types: %q must be one of Fandom, Relationship, Character, Freeform", t)
		}
	}
	if c.Images.PerHostInterval < time.Second {
		add("images.per_host_interval must be >= 1s")
	}
	if c.Images.MaxBytes < 1 {
		add("images.max_bytes must be >= 1")
	}
	if c.Content.KeepVersions < 1 {
		add("content.keep_versions must be >= 1")
	}
	errs = append(errs, c.Auth.validate()...)
	errs = append(errs, c.API.validate()...)
	return errors.Join(errs...)
}

func (a Auth) validate() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if a.AccessTTL < MinAccessTTL || a.AccessTTL > MaxAccessTTL {
		add("auth.access_ttl %s must be between %s and %s", a.AccessTTL, MinAccessTTL, MaxAccessTTL)
	}
	if a.RefreshTTL < MinRefreshTTL || a.RefreshTTL > MaxRefreshTTL {
		add("auth.refresh_ttl %s must be between %s and %s", a.RefreshTTL, MinRefreshTTL, MaxRefreshTTL)
	}
	if a.MaxSessionAge < a.RefreshTTL || a.MaxSessionAge > MaxMaxSessionAge {
		add("auth.max_session_age %s must be between auth.refresh_ttl (%s) and %s", a.MaxSessionAge, a.RefreshTTL, MaxMaxSessionAge)
	}
	if a.InviteTTL < MinInviteTTL || a.InviteTTL > MaxInviteTTL {
		add("auth.invite_ttl %s must be between %s and %s", a.InviteTTL, MinInviteTTL, MaxInviteTTL)
	}
	if a.SigningKeyRotation < MinSigningKeyRotation || a.SigningKeyRotation > MaxSigningKeyRotation {
		add("auth.signing_key_rotation %s must be between %s and %s", a.SigningKeyRotation, MinSigningKeyRotation, MaxSigningKeyRotation)
	}
	if a.MasterKeyRotation < MinMasterKeyRotation || a.MasterKeyRotation > MaxMasterKeyRotation {
		add("auth.master_key_rotation %s must be between %s and %s", a.MasterKeyRotation, MinMasterKeyRotation, MaxMasterKeyRotation)
	}
	if !totpIssuer.MatchString(a.TOTPIssuer) {
		add("auth.totp_issuer %q must be 1-32 of letters, digits, space, '.', '_' or '-', not starting or ending with a space", a.TOTPIssuer)
	}
	return errs
}

// validatePublicURL checks api.public_url: an http(s) origin with a lower-case host from
// allowed_hosts (the invite links built from it must match dto.InviteURL).
func (a API) validatePublicURL() error {
	u, err := url.Parse(a.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(a.PublicURL, "#") {
		return fmt.Errorf("api.public_url %q must be an http(s) origin with no path, query or fragment (e.g. https://mirror.example.com)", a.PublicURL)
	}
	host := u.Hostname()
	if host != strings.ToLower(host) || !ValidHost(host) {
		return fmt.Errorf("api.public_url %q: the host must be a lower-case host name or IP address", a.PublicURL)
	}
	if port := u.Port(); port != "" {
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("api.public_url %q: port must be 1-65535", a.PublicURL)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("api.public_url %q: empty port", a.PublicURL)
	}
	for _, h := range a.AllowedHosts {
		if strings.EqualFold(h, host) {
			return nil
		}
	}
	return fmt.Errorf("api.public_url %q: host %q must be in api.allowed_hosts", a.PublicURL, host)
}

func (a API) validate() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if host, port, err := net.SplitHostPort(a.Listen); err != nil || host == "" || port == "" {
		add("api.listen %q must be host:port (e.g. 127.0.0.1:8080)", a.Listen)
	} else if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		add("api.listen %q: port must be 1-65535", a.Listen)
	}
	if !filepath.IsLocal(a.AdminSocket) { // also rejects "", absolute paths and ".." escapes
		add("api.admin_socket %q must be a relative path inside data_dir", a.AdminSocket)
	}
	if len(a.TrustedProxies) == 0 {
		add("api.trusted_proxies must not be empty (default [127.0.0.1/32, ::1/128])")
	}
	for _, p := range a.TrustedProxies {
		if _, err := netip.ParsePrefix(p); err != nil {
			add("api.trusted_proxies: %q is not a CIDR prefix", p)
		}
	}
	if len(a.AllowedHosts) == 0 {
		add("api.allowed_hosts must not be empty (default [127.0.0.1, ::1, localhost])")
	}
	for _, h := range a.AllowedHosts {
		if !ValidHost(h) {
			add("api.allowed_hosts: %q is not a host name or IP address (no port, no wildcard)", h)
		}
	}
	if a.RequestTimeout < time.Second || a.RequestTimeout > MaxRequestTimeout {
		add("api.request_timeout %s must be between 1s and %s", a.RequestTimeout, MaxRequestTimeout)
	}
	if a.HashKeyRotation < 0 || (a.HashKeyRotation > 0 && a.HashKeyRotation < redact.MinRotation) {
		add("api.hash_key_rotation %s must be 0 (once per process) or >= %s", a.HashKeyRotation, redact.MinRotation)
	}
	if r := a.Rate; r.GeneralPerMin < 1 || r.UIPerMin < 1 || r.SearchPerMin < 1 || r.SourceAddPerHour < 1 ||
		r.SignupPerHour < 1 || r.LoginPerMin < 1 || r.RefreshPerMin < 1 {
		add("api.rate.general_per_min, ui_per_min, search_per_min, source_add_per_hour, signup_per_hour, " +
			"login_per_min and refresh_per_min must be >= 1")
	}
	if err := a.validatePublicURL(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// hostLabel is one DNS label: letters, digits and inner hyphens, at most 63 bytes.
var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidHost reports whether s is an IP address (no zone) or a DNS name of at most 253 bytes
// with no trailing dot: an entry of api.allowed_hosts.
func ValidHost(s string) bool {
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.Zone() == ""
	}
	if s == "" || len(s) > 253 {
		return false
	}
	for l := range strings.SplitSeq(s, ".") {
		if !hostLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// UserAgent builds the User-Agent sent on every request. It always includes the contact.
func (c Config) UserAgent(version string) string {
	ua := fmt.Sprintf("personal-ao3-mirror/%s (personal offline archive; contact: discord %s)", version, c.Contact.Discord)
	if s := strings.TrimSpace(c.UserAgentSuffix); s != "" {
		ua += " " + s
	}
	return ua
}

// Path resolves p relative to DataDir.
func (c Config) Path(p ...string) string {
	return filepath.Join(append([]string{c.DataDir}, p...)...)
}
