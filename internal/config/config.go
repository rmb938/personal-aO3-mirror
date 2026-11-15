// Package config loads and validates ao3mirror.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
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
	Images          Images  `yaml:"images"`
	Content         Content `yaml:"content"`
	Auth            Auth    `yaml:"auth"`
}

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
}

type Images struct {
	Enabled         bool          `yaml:"enabled"`
	PerHostInterval time.Duration `yaml:"per_host_interval"`
	MaxBytes        int64         `yaml:"max_bytes"`
}

type Content struct {
	KeepVersions int `yaml:"keep_versions"`
}

type Auth struct {
	Enabled bool `yaml:"enabled"`
}

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
		Tags:    Tags{InfoTTL: 30 * 24 * time.Hour},
		Images:  Images{Enabled: true, PerHostInterval: 3 * time.Second, MaxBytes: 25 << 20},
		Content: Content{KeepVersions: 1},
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
	if c.Images.PerHostInterval < time.Second {
		add("images.per_host_interval must be >= 1s")
	}
	if c.Images.MaxBytes < 1 {
		add("images.max_bytes must be >= 1")
	}
	if c.Content.KeepVersions < 1 {
		add("content.keep_versions must be >= 1")
	}
	if c.Auth.Enabled {
		add("auth.enabled: login is not implemented yet")
	}
	return errors.Join(errs...)
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
