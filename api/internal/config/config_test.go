package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Server.Port != 9393 {
		t.Errorf("default port = %d, want 9393", c.Server.Port)
	}
	if c.Server.Host != "0.0.0.0" {
		t.Errorf("default host = %q, want 0.0.0.0", c.Server.Host)
	}
}

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	t.Setenv("VAC_PORT", "8080")
	t.Setenv("VAC_HOST", "127.0.0.1")
	t.Setenv("VAC_DATABASE_URL", "postgres://test")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("host = %q", cfg.Server.Host)
	}
	if cfg.DatabaseURL != "postgres://test" {
		t.Errorf("database url = %q", cfg.DatabaseURL)
	}
}

func TestDefault_Phase3(t *testing.T) {
	c := Default()
	if c.CaddyAdminURL != "http://vac-proxy:2019" {
		t.Errorf("caddy admin url = %q", c.CaddyAdminURL)
	}
	if c.EdgeNetwork != "vac-edge" {
		t.Errorf("edge network = %q", c.EdgeNetwork)
	}
	if c.BaseDomain != "" {
		t.Errorf("base domain default = %q, want empty", c.BaseDomain)
	}
	if c.RequestMetricsRetention != 24*time.Hour {
		t.Errorf("request metrics retention = %v", c.RequestMetricsRetention)
	}
	if c.CaddyMetricsInterval != 10*time.Second {
		t.Errorf("caddy metrics interval = %v", c.CaddyMetricsInterval)
	}
}

func TestLoad_Phase3EnvOverrides(t *testing.T) {
	t.Setenv("VAC_DATABASE_URL", "postgres://test")
	t.Setenv("VAC_BASE_DOMAIN", "vac.example.com")
	t.Setenv("VAC_EDGE_NETWORK", "custom-edge")
	t.Setenv("VAC_CADDY_ADMIN_URL", "http://localhost:2020")
	t.Setenv("VAC_CADDY_ASK_TOKEN", "secret")
	t.Setenv("VAC_REQUEST_METRICS_RETENTION", "12h")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseDomain != "vac.example.com" {
		t.Errorf("base domain = %q", cfg.BaseDomain)
	}
	if cfg.EdgeNetwork != "custom-edge" {
		t.Errorf("edge network = %q", cfg.EdgeNetwork)
	}
	if cfg.CaddyAdminURL != "http://localhost:2020" {
		t.Errorf("caddy admin url = %q", cfg.CaddyAdminURL)
	}
	if cfg.CaddyAskToken != "secret" {
		t.Errorf("ask token = %q", cfg.CaddyAskToken)
	}
	if cfg.RequestMetricsRetention != 12*time.Hour {
		t.Errorf("request metrics retention = %v", cfg.RequestMetricsRetention)
	}
}

func TestLoad_YAMLThenEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vac.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 4000\n  host: yamlhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAC_CONFIG_FILE", path)
	t.Setenv("VAC_PORT", "5000") // env wins over yaml

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 5000 {
		t.Errorf("env should override yaml: port = %d, want 5000", cfg.Server.Port)
	}
	if cfg.Server.Host != "yamlhost" {
		t.Errorf("yaml should override default: host = %q", cfg.Server.Host)
	}
}

func TestLoad_MasterKeyValid(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv("VAC_MASTER_KEY", hex.EncodeToString(key))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MasterKey) != 32 {
		t.Fatalf("master key length = %d, want 32", len(cfg.MasterKey))
	}
}

func TestLoad_MasterKeyInvalid_DoesNotFail(t *testing.T) {
	t.Setenv("VAC_MASTER_KEY", "not-hex")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("malformed key should warn, not error: %v", err)
	}
	if cfg.MasterKey != nil {
		t.Error("master key should be nil when malformed")
	}
}

func TestAddr(t *testing.T) {
	c := Config{Server: ServerConfig{Host: "1.2.3.4", Port: 9000}}
	if c.Addr() != "1.2.3.4:9000" {
		t.Errorf("Addr() = %q", c.Addr())
	}
}

func TestExposureAndSessionDefaults(t *testing.T) {
	c := Default()
	if c.Exposure != ExposurePublic {
		t.Errorf("default exposure = %q", c.Exposure)
	}
	if !c.SecureCookies() {
		t.Error("default exposure should require secure cookies")
	}
	if c.SessionTTL == 0 || c.SessionTTLExtended <= c.SessionTTL {
		t.Errorf("session ttls: %v / %v", c.SessionTTL, c.SessionTTLExtended)
	}
}

func TestLoad_ExposureLocalDropsSecure(t *testing.T) {
	t.Setenv("VAC_EXPOSURE", "local")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Exposure != ExposureLocal {
		t.Errorf("exposure = %q", cfg.Exposure)
	}
	if cfg.SecureCookies() {
		t.Error("local exposure must not require secure cookies")
	}
}

func TestLoad_InvalidExposureFallsBack(t *testing.T) {
	t.Setenv("VAC_EXPOSURE", "garbage")
	cfg, _ := Load()
	if cfg.Exposure != ExposurePublic {
		t.Errorf("invalid exposure should fall back to public, got %q", cfg.Exposure)
	}
}

func TestLoad_ControlDomainDefaultsFromBaseDomain(t *testing.T) {
	t.Setenv("VAC_BASE_DOMAIN", "example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlDomain != "vac.example.com" {
		t.Errorf("control domain = %q, want vac.example.com", cfg.ControlDomain)
	}
}

func TestLoad_ControlDomainExplicitOverride(t *testing.T) {
	t.Setenv("VAC_BASE_DOMAIN", "example.com")
	t.Setenv("VAC_CONTROL_DOMAIN", "admin.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlDomain != "admin.example.com" {
		t.Errorf("control domain = %q, want admin.example.com", cfg.ControlDomain)
	}
}

func TestLoad_ControlDomainEmptyWithoutBaseDomain(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlDomain != "" {
		t.Errorf("control domain = %q, want empty when no base domain", cfg.ControlDomain)
	}
}

func TestLoad_SessionTTLs(t *testing.T) {
	t.Setenv("VAC_SESSION_TTL", "2h")
	t.Setenv("VAC_SESSION_TTL_EXTENDED", "48h")
	cfg, _ := Load()
	if cfg.SessionTTL.Hours() != 2 {
		t.Errorf("session ttl = %v", cfg.SessionTTL)
	}
	if cfg.SessionTTLExtended.Hours() != 48 {
		t.Errorf("session ttl extended = %v", cfg.SessionTTLExtended)
	}
}

func TestLoad_LoginRateLimit(t *testing.T) {
	t.Setenv("VAC_LOGIN_RATE_LIMIT", "10")
	t.Setenv("VAC_LOGIN_RATE_WINDOW", "30m")
	cfg, _ := Load()
	if cfg.LoginRateLimit != 10 {
		t.Errorf("login rate limit = %d, want 10", cfg.LoginRateLimit)
	}
	if cfg.LoginRateWindow != 30*time.Minute {
		t.Errorf("login rate window = %v, want 30m", cfg.LoginRateWindow)
	}
}
