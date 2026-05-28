package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Server.Port != 3000 {
		t.Errorf("default port = %d, want 3000", c.Server.Port)
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
