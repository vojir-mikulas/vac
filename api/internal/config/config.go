// Package config loads VAC configuration.
//
// Precedence (lowest → highest): hardcoded defaults → vac.yaml → environment variables.
// Env vars always win. Secrets are env-only — never read from the config file.
package config

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Exposure mode controls cookie Secure flag and bind behaviour.
const (
	ExposurePublic = "public"
	ExposureLocal  = "local"
)

type Config struct {
	Server             ServerConfig  `yaml:"server"`
	DatabaseURL        string        `yaml:"-"` // env-only (VAC_DATABASE_URL)
	MasterKey          []byte        `yaml:"-"` // env-only (VAC_MASTER_KEY), 32 bytes
	Exposure           string        `yaml:"exposure"`
	SessionTTL         time.Duration `yaml:"session_ttl"`
	SessionTTLExtended time.Duration `yaml:"session_ttl_extended"`
	LoginRateLimit     int           `yaml:"login_rate_limit"`
	LoginRateWindow    time.Duration `yaml:"login_rate_window"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Port: 3000,
			Host: "0.0.0.0",
		},
		Exposure:           ExposurePublic,
		SessionTTL:         7 * 24 * time.Hour,
		SessionTTLExtended: 30 * 24 * time.Hour,
		LoginRateLimit:     5,
		LoginRateWindow:    15 * time.Minute,
	}
}

// Load returns a Config built from defaults, an optional yaml file (path from
// VAC_CONFIG_FILE), and environment variables, in that order of precedence.
func Load() (Config, error) {
	cfg := Default()

	if path := os.Getenv("VAC_CONFIG_FILE"); path != "" {
		if err := loadYAML(path, &cfg); err != nil {
			return cfg, fmt.Errorf("config: %w", err)
		}
	}

	applyEnv(&cfg)

	validate(&cfg)
	return cfg, nil
}

func loadYAML(path string, cfg *Config) error {
	f, err := os.Open(path) //nolint:gosec // path is operator-controlled config
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("VAC_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = n
		}
	}
	if v := os.Getenv("VAC_HOST"); v != "" {
		cfg.Server.Host = v
	}
	cfg.DatabaseURL = os.Getenv("VAC_DATABASE_URL")

	if v := os.Getenv("VAC_MASTER_KEY"); v != "" {
		key, err := hex.DecodeString(v)
		if err != nil || len(key) != 32 {
			slog.Warn("VAC_MASTER_KEY is malformed (expected 32 bytes hex) — encryption disabled until corrected")
		} else {
			cfg.MasterKey = key
		}
	}

	if v := os.Getenv("VAC_EXPOSURE"); v != "" {
		cfg.Exposure = v
	}
	if v := os.Getenv("VAC_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionTTL = d
		}
	}
	if v := os.Getenv("VAC_SESSION_TTL_EXTENDED"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionTTLExtended = d
		}
	}
	if v := os.Getenv("VAC_LOGIN_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LoginRateLimit = n
		}
	}
	if v := os.Getenv("VAC_LOGIN_RATE_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.LoginRateWindow = d
		}
	}
}

func validate(cfg *Config) {
	if len(cfg.MasterKey) == 0 {
		slog.Warn("VAC_MASTER_KEY is not set — encryption disabled, app creation will be blocked")
	}
	if cfg.Exposure != ExposurePublic && cfg.Exposure != ExposureLocal {
		slog.Warn("VAC_EXPOSURE is invalid; falling back to public", "value", cfg.Exposure)
		cfg.Exposure = ExposurePublic
	}
}

// Addr returns the host:port string used by the HTTP server.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// SecureCookies returns true when cookies must carry the Secure flag (HTTPS
// required). Local-exposure deployments behind Tailscale / SSH-tunnel do not
// need it.
func (c Config) SecureCookies() bool {
	return c.Exposure == ExposurePublic
}
