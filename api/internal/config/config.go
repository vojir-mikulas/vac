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

	// Phase 2: deployment pipeline configuration.
	WorkDir               string        `yaml:"work_dir"`
	DockerSocket          string        `yaml:"docker_socket"`
	ImageKeepCount        int           `yaml:"image_keep_count"`
	HealthCheckTimeout    time.Duration `yaml:"health_check_timeout"`
	HealthCheckRetries    int           `yaml:"health_check_retries"`
	CrashLoopThreshold    int           `yaml:"crash_loop_threshold"`
	CrashLoopWindow       time.Duration `yaml:"crash_loop_window"`
	LogRetentionDays      int           `yaml:"log_retention_days"`
	ActivityRetentionDays int           `yaml:"activity_retention_days"`
	LogRingBuffer         int           `yaml:"ring_buffer_lines"`

	// Phase 3: reverse proxy & HTTPS.
	CaddyAdminURL           string        `yaml:"caddy_admin_url"`
	BaseDomain              string        `yaml:"base_domain"`
	EdgeNetwork             string        `yaml:"edge_network"`
	CaddyAccessLog          string        `yaml:"caddy_access_log"`
	CaddyMetricsInterval    time.Duration `yaml:"caddy_metrics_interval"`
	StatsPollInterval       time.Duration `yaml:"stats_poll_interval"`
	CaddyAskToken           string        `yaml:"-"` // env-only secret (VAC_CADDY_ASK_TOKEN)
	RequestMetricsRetention time.Duration `yaml:"request_metrics_retention"`
	ACMECA                  string        `yaml:"acme_ca"` // override for ACME staging in tests

	// Phase 4: notifications. Webhook URLs are semi-secret — env-only, never in
	// the config file; they override any UI-stored value.
	NotifyDiscordURL string `yaml:"-"`
	NotifySlackURL   string `yaml:"-"`
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

		WorkDir:               "/var/lib/vac/repos",
		DockerSocket:          "/var/run/docker.sock",
		ImageKeepCount:        3,
		HealthCheckTimeout:    30 * time.Second,
		HealthCheckRetries:    5,
		CrashLoopThreshold:    5,
		CrashLoopWindow:       2 * time.Minute,
		LogRetentionDays:      7,
		ActivityRetentionDays: 30,
		LogRingBuffer:         10000,

		CaddyAdminURL:           "http://vac-proxy:2019",
		BaseDomain:              "",
		EdgeNetwork:             "vac-edge",
		CaddyAccessLog:          "/var/log/caddy/access.log",
		CaddyMetricsInterval:    10 * time.Second,
		StatsPollInterval:       2 * time.Second,
		RequestMetricsRetention: 24 * time.Hour,
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

	if v := os.Getenv("VAC_WORK_DIR"); v != "" {
		cfg.WorkDir = v
	}
	if v := os.Getenv("VAC_DOCKER_SOCKET"); v != "" {
		cfg.DockerSocket = v
	}
	if v := os.Getenv("VAC_IMAGE_KEEP_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ImageKeepCount = n
		}
	}
	if v := os.Getenv("VAC_HEALTH_CHECK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.HealthCheckTimeout = d
		}
	}
	if v := os.Getenv("VAC_HEALTH_CHECK_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.HealthCheckRetries = n
		}
	}
	if v := os.Getenv("VAC_CRASH_LOOP_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CrashLoopThreshold = n
		}
	}
	if v := os.Getenv("VAC_CRASH_LOOP_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.CrashLoopWindow = d
		}
	}
	if v := os.Getenv("VAC_LOG_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LogRetentionDays = n
		}
	}
	if v := os.Getenv("VAC_ACTIVITY_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ActivityRetentionDays = n
		}
	}
	if v := os.Getenv("VAC_LOG_RING_BUFFER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LogRingBuffer = n
		}
	}

	if v := os.Getenv("VAC_CADDY_ADMIN_URL"); v != "" {
		cfg.CaddyAdminURL = v
	}
	if v := os.Getenv("VAC_BASE_DOMAIN"); v != "" {
		cfg.BaseDomain = v
	}
	if v := os.Getenv("VAC_EDGE_NETWORK"); v != "" {
		cfg.EdgeNetwork = v
	}
	if v := os.Getenv("VAC_CADDY_ACCESS_LOG"); v != "" {
		cfg.CaddyAccessLog = v
	}
	if v := os.Getenv("VAC_CADDY_METRICS_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.CaddyMetricsInterval = d
		}
	}
	if v := os.Getenv("VAC_STATS_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.StatsPollInterval = d
		}
	}
	if v := os.Getenv("VAC_CADDY_ASK_TOKEN"); v != "" {
		cfg.CaddyAskToken = v
	}
	if v := os.Getenv("VAC_REQUEST_METRICS_RETENTION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.RequestMetricsRetention = d
		}
	}
	if v := os.Getenv("VAC_ACME_CA"); v != "" {
		cfg.ACMECA = v
	}
	cfg.NotifyDiscordURL = os.Getenv("VAC_NOTIFY_DISCORD_URL")
	cfg.NotifySlackURL = os.Getenv("VAC_NOTIFY_SLACK_URL")
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
