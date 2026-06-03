// Package config loads VAC configuration.
//
// Precedence (lowest → highest): hardcoded defaults → vac.yaml → environment variables.
// Env vars always win. Secrets are env-only — never read from the config file.
package config

import (
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
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
	DeploymentKeepCount   int           `yaml:"deployment_keep_count"`

	// Phase 3: reverse proxy & HTTPS.
	CaddyAdminURL string `yaml:"caddy_admin_url"`
	BaseDomain    string `yaml:"base_domain"`
	// BaseDomainSource records where a non-empty BaseDomain came from ("env" or
	// "file"), so the Domains settings card can label the effective value's
	// origin. Computed in Load() — never read from env/yaml. Empty means no base
	// domain is configured at the config layer.
	BaseDomainSource        string        `yaml:"-"`
	ControlDomain           string        `yaml:"control_domain"`
	EdgeNetwork             string        `yaml:"edge_network"`
	CaddyAccessLog          string        `yaml:"caddy_access_log"`
	CaddyMetricsInterval    time.Duration `yaml:"caddy_metrics_interval"`
	StatsPollInterval       time.Duration `yaml:"stats_poll_interval"`
	CaddyAskToken           string        `yaml:"-"` // env-only secret (VAC_CADDY_ASK_TOKEN)
	RequestMetricsRetention time.Duration `yaml:"request_metrics_retention"`
	ACMECA                  string        `yaml:"acme_ca"` // override for ACME staging in tests

	// Cert-expiry notification (plan 03). CertExpiryDays is the alert window —
	// a managed host whose TLS cert is within this many days of expiry (and
	// hasn't auto-renewed) fires one notification. CertProbeAddr is the
	// host:port the checker TLS-dials with each host's SNI to read the real
	// expiry; empty derives "<caddy-admin-host>:443" from CaddyAdminURL.
	CertExpiryDays int    `yaml:"cert_expiry_days"`
	CertProbeAddr  string `yaml:"cert_probe_addr"`

	// Track B (observability): bearer token gating /metrics and /debug/* — both
	// leak instance topology / runtime internals, so they are default-closed.
	// Env-only secret (VAC_METRICS_TOKEN); unset → those endpoints return 404.
	MetricsToken string `yaml:"-"`

	// PublicIP is the VPS's public address, surfaced in the dashboard (sidebar
	// host row) and used by the per-app DNS-setup guidance so operators see the
	// exact A-record value. Empty triggers auto-detection: the local outbound IP
	// is used when it is public, otherwise an external IP-echo service is queried
	// over HTTPS to learn the true public IP (covers NAT'd hosts).
	PublicIP string `yaml:"public_ip"`

	// Phase 4: notifications. Webhook URLs are semi-secret — env-only, never in
	// the config file; they override any UI-stored value.
	NotifyDiscordURL string `yaml:"-"`
	NotifySlackURL   string `yaml:"-"`

	// Track D (managed services). ManagedServices is the master gate: when off
	// (the default) the backup scheduler / managed-DB goroutines never start,
	// the nav entries stay hidden, and the <200 MB control-plane claim holds.
	// ManagedDBIsolated points managed Postgres at a second vac-db-managed
	// instance for blast-radius isolation instead of sharing the control-plane DB.
	ManagedServices   bool `yaml:"managed_services"`
	ManagedDBIsolated bool `yaml:"managed_db_isolated"`

	// Track E / E2 (security dashboard). SecurityMonitor gates the always-on
	// traffic-anomaly detector that rides the existing Caddy access-log tail
	// (default on — it holds only bounded in-memory counters, near-zero cost).
	// The thresholds tune anomaly detection so a busy small app doesn't
	// false-positive: an IP must exceed SecurityRPSThreshold requests in the
	// sliding window, or a single IP must exceed SecurityErrThreshold 4xx/5xx
	// responses, to trip an alert; SecurityCooldown debounces repeat alerts.
	SecurityMonitor      bool          `yaml:"security_monitor"`
	SecurityRPSThreshold int           `yaml:"security_rps_threshold"`
	SecurityErrThreshold int           `yaml:"security_err_threshold"`
	SecurityWindow       time.Duration `yaml:"security_window"`
	SecurityCooldown     time.Duration `yaml:"security_cooldown"`

	// Build metadata injected by main() from ldflags vars; surfaced by the
	// instance-info endpoint. Not read from env/yaml.
	Version   string `yaml:"-"`
	Commit    string `yaml:"-"`
	BuildDate string `yaml:"-"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			// 9393 is a deliberately uncommon control-plane port: 3000 is the most
			// contested app dev port, so claiming it as the dashboard default
			// invites collisions on a fresh box. Override with VAC_PORT.
			Port: 9393,
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
		DeploymentKeepCount:   20,

		CaddyAdminURL:           "http://vac-proxy:2019",
		BaseDomain:              "",
		EdgeNetwork:             "vac-edge",
		CaddyAccessLog:          "/var/log/caddy/access.log",
		CaddyMetricsInterval:    10 * time.Second,
		StatsPollInterval:       2 * time.Second,
		RequestMetricsRetention: 24 * time.Hour,
		CertExpiryDays:          14,

		SecurityMonitor:      true,
		SecurityRPSThreshold: 300, // requests from one IP within the window
		SecurityErrThreshold: 100, // 4xx/5xx from one IP within the window
		SecurityWindow:       time.Minute,
		SecurityCooldown:     10 * time.Minute,
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

	deriveBaseDomainSource(&cfg)
	validate(&cfg)
	return cfg, nil
}

// deriveBaseDomainSource records where the effective base domain came from so the
// UI can label it. Default() leaves BaseDomain empty, so after the yaml+env merge
// a non-empty value with no VAC_BASE_DOMAIN set could only have come from the
// config file. Leaves the source empty when no base domain is configured.
func deriveBaseDomainSource(cfg *Config) {
	switch {
	case os.Getenv("VAC_BASE_DOMAIN") != "":
		cfg.BaseDomainSource = "env"
	case cfg.BaseDomain != "":
		cfg.BaseDomainSource = "file"
	}
}

func loadYAML(path string, cfg *Config) error {
	f, err := os.Open(path) //nolint:gosec // path is operator-controlled config
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
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
	if v := os.Getenv("VAC_DEPLOYMENT_KEEP_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DeploymentKeepCount = n
		}
	}

	if v := os.Getenv("VAC_CADDY_ADMIN_URL"); v != "" {
		cfg.CaddyAdminURL = v
	}
	if v := os.Getenv("VAC_CERT_EXPIRY_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CertExpiryDays = n
		}
	}
	if v := os.Getenv("VAC_CERT_PROBE_ADDR"); v != "" {
		cfg.CertProbeAddr = v
	}
	if v := os.Getenv("VAC_BASE_DOMAIN"); v != "" {
		cfg.BaseDomain = v
	}
	if v := os.Getenv("VAC_CONTROL_DOMAIN"); v != "" {
		cfg.ControlDomain = v
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
	if v := os.Getenv("VAC_METRICS_TOKEN"); v != "" {
		cfg.MetricsToken = v
	}
	if v := os.Getenv("VAC_REQUEST_METRICS_RETENTION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.RequestMetricsRetention = d
		}
	}
	if v := os.Getenv("VAC_ACME_CA"); v != "" {
		cfg.ACMECA = v
	}
	if v := os.Getenv("VAC_PUBLIC_IP"); v != "" {
		cfg.PublicIP = v
	}
	cfg.NotifyDiscordURL = os.Getenv("VAC_NOTIFY_DISCORD_URL")
	cfg.NotifySlackURL = os.Getenv("VAC_NOTIFY_SLACK_URL")

	if v := os.Getenv("VAC_MANAGED_SERVICES"); v != "" {
		cfg.ManagedServices = v == "true" || v == "1"
	}
	if v := os.Getenv("VAC_MANAGED_DB_ISOLATED"); v != "" {
		cfg.ManagedDBIsolated = v == "true" || v == "1"
	}

	if v := os.Getenv("VAC_SECURITY_MONITOR"); v != "" {
		cfg.SecurityMonitor = v == "true" || v == "1"
	}
	if v := os.Getenv("VAC_SECURITY_RPS_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.SecurityRPSThreshold = n
		}
	}
	if v := os.Getenv("VAC_SECURITY_ERR_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.SecurityErrThreshold = n
		}
	}
	if v := os.Getenv("VAC_SECURITY_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.SecurityWindow = d
		}
	}
	if v := os.Getenv("VAC_SECURITY_COOLDOWN"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.SecurityCooldown = d
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
	// Derive the default control-plane hostname from BaseDomain when the
	// operator hasn't pinned one. vac.<domain> keeps the apex free for an
	// app or marketing page; VAC_CONTROL_DOMAIN overrides (apex included).
	if cfg.ControlDomain == "" && cfg.BaseDomain != "" {
		cfg.ControlDomain = "vac." + cfg.BaseDomain
	}
}

// Addr returns the host:port string used by the HTTP server.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// PublicIPAddr returns the configured public IP verbatim when set (no network
// call). When unset it auto-detects the host's public address: the local
// outbound-interface IP is used if it is already public (fast path, no egress);
// if that IP is private/loopback/link-local/CGNAT it reaches out to an external
// IP-echo service over HTTPS to learn the true public IP, falling back to the
// local IP so the dashboard still shows something. The auto-detected result is
// cached for the process lifetime; it returns "" only if every step fails.
func (c Config) PublicIPAddr() string {
	if c.PublicIP != "" {
		return c.PublicIP
	}
	return autoDetectPublicIP()
}

// autoDetectPublicIP runs the local-then-external-echo detection at most once
// per process and caches the result (PublicIPAddr is called from both main and
// the server wiring).
var autoDetectPublicIP = sync.OnceValue(func() string {
	local := detectOutboundIP()
	if isPublicIP(local) {
		return local
	}
	if ip := publicIPFrom(&http.Client{Timeout: 4 * time.Second}, defaultEchoURLs); ip != "" {
		return ip
	}
	return local
})

// defaultEchoURLs are plaintext IP-echo endpoints queried in order to learn the
// host's true public IP when the local interface address is not public.
var defaultEchoURLs = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

// detectOutboundIP finds the local IP used for outbound traffic. The UDP "dial"
// only selects a route (no packets are sent) so it's cheap and offline-safe; it
// returns "" if no route can be determined.
func detectOutboundIP() string {
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// cgnatNet is the RFC 6598 carrier-grade NAT range (100.64.0.0/10).
var cgnatNet = netip.MustParsePrefix("100.64.0.0/10")

// isPublicIP reports whether s parses to a routable public IP. It returns false
// for invalid input, the unspecified address, loopback, link-local
// (unicast/multicast), RFC1918 private ranges, IPv6 unique-local (fc00::/7), and
// CGNAT (100.64.0.0/10).
func isPublicIP(s string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if addr.IsUnspecified() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsPrivate() || // RFC1918 (v4) and fc00::/7 (v6)
		cgnatNet.Contains(addr) {
		return false
	}
	return true
}

// publicIPFrom GETs each URL in order with the client's timeout, returning the
// first response body that trims to a valid public IP. Network failures,
// non-200 responses, and non-public bodies are skipped; it never panics and
// returns "" if none succeed.
func publicIPFrom(client *http.Client, urls []string) string {
	for _, url := range urls {
		if ip := echoLookup(client, url); ip != "" {
			return ip
		}
	}
	return ""
}

func echoLookup(client *http.Client, url string) string {
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	candidate := strings.TrimSpace(string(body))
	if isPublicIP(candidate) {
		return candidate
	}
	return ""
}

// SecureCookies returns true when cookies must carry the Secure flag (HTTPS
// required). Local-exposure deployments behind Tailscale / SSH-tunnel do not
// need it.
func (c Config) SecureCookies() bool {
	return c.Exposure == ExposurePublic
}
