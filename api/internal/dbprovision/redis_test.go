package dbprovision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func redisEngine(fd *fakeDocker, workDir string) *RedisEngine {
	return NewRedisEngine(fd, Config{
		WorkDir:     workDir,
		EdgeNetwork: "vac-edge",
		MasterKey:   []byte("0123456789abcdef0123456789abcdef"),
	})
}

func TestRedisEngine_ComposeYAML(t *testing.T) {
	y := redisEngine(&fakeDocker{}, t.TempDir()).composeYAML()
	for _, want := range []string{"vac-redis", "redis:7-alpine", "external: true", "vac-edge", "--appendonly yes", "--aclfile /data/users.acl"} {
		if !strings.Contains(y, want) {
			t.Errorf("compose yaml missing %q:\n%s", want, y)
		}
	}
	// The generated compose must be valid YAML — the bootstrap command uses a
	// block scalar, which is easy to mis-indent.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("compose yaml does not parse: %v\n%s", err, y)
	}
}

func TestRedisEngine_EnsureRunning(t *testing.T) {
	fd := &fakeDocker{}
	dir := t.TempDir()
	e := redisEngine(fd, dir)
	if err := e.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "managed", "redis", "compose.yaml")); err != nil {
		t.Errorf("compose file not written: %v", err)
	}
	if len(fd.ups) != 1 || fd.ups[0] != "vac-managed-redis" {
		t.Errorf("up not called with project: %v", fd.ups)
	}
	if joined := strings.Join(fd.execs, "\n"); !strings.Contains(joined, "PING") {
		t.Errorf("no readiness ping: %v", fd.execs)
	}
}

func TestRedisEngine_ProvisionCommand(t *testing.T) {
	fd := &fakeDocker{}
	e := redisEngine(fd, t.TempDir())
	if err := e.Provision(context.Background(), "cache_abc", "cache_abc_u", "pw123"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	cmd := fd.execs[len(fd.execs)-1]
	for _, want := range []string{
		"ACL SETUSER cache_abc_u reset on",
		"'>pw123'",       // password set, shell-quoted so '>' isn't redirection
		"'~cache_abc:*'", // key-prefix isolation
		"'&cache_abc:*'", // channel-prefix isolation
		"+@all -@dangerous",
		"ACL SAVE",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("provision command missing %q:\n%s", want, cmd)
		}
	}
	// No backticks — they'd trigger command substitution in the container shell.
	if strings.Contains(cmd, "`") {
		t.Errorf("provision command must not contain backticks: %s", cmd)
	}
}

func TestRedisEngine_DeprovisionCommand(t *testing.T) {
	fd := &fakeDocker{}
	e := redisEngine(fd, t.TempDir())
	if err := e.Deprovision(context.Background(), "cache_abc", "cache_abc_u"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	cmd := fd.execs[len(fd.execs)-1]
	for _, want := range []string{"--scan --pattern 'cache_abc:*'", "ACL DELUSER cache_abc_u", "ACL SAVE"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("deprovision command missing %q:\n%s", want, cmd)
		}
	}
}

func TestRedisEngine_ConnStringAndCapabilities(t *testing.T) {
	e := redisEngine(&fakeDocker{}, t.TempDir())
	if got := e.ConnString("cache_abc", "cache_abc_u", "pw"); got != "redis://cache_abc_u:pw@vac-redis:6379" {
		t.Errorf("conn string = %q", got)
	}
	if !e.Shared() || e.FootprintMB() == 0 {
		t.Errorf("redis should be shared + carry a footprint")
	}
	// Opts out of backups and never matches a backup command, so restore/verify
	// are never offered for Redis.
	if !e.SkipsBackup() {
		t.Errorf("redis should opt out of backups")
	}
	if _, ok := e.MatchBackupCommand("anything at all"); ok {
		t.Errorf("redis must never match a backup command")
	}
	// SizeBytes reports unknown (empty), never an error.
	sz, err := e.SizeBytes(context.Background(), []string{"cache_abc"})
	if err != nil || len(sz) != 0 {
		t.Errorf("SizeBytes = %v, %v; want empty map, nil", sz, err)
	}
}

func TestRedisEngine_ExtraEnv(t *testing.T) {
	e := redisEngine(&fakeDocker{}, t.TempDir())
	got := e.ExtraEnv("REDIS_URL", GeneratedNames{DBName: "cache_abc"})
	if got["REDIS_PREFIX"] != "cache_abc:" {
		t.Errorf("ExtraEnv = %v, want REDIS_PREFIX=cache_abc:", got)
	}
	// A non-_URL binding falls back to <binding>_PREFIX.
	if v := redisPrefixVar("CACHE"); v != "CACHE_PREFIX" {
		t.Errorf("redisPrefixVar(CACHE) = %q", v)
	}
	if v := redisPrefixVar("ANALYTICS_URL"); v != "ANALYTICS_PREFIX" {
		t.Errorf("redisPrefixVar(ANALYTICS_URL) = %q", v)
	}
}

// TestProvisioner_RedisSkipsBackupAndInjectsPrefix exercises the optional-interface
// wiring end to end through provision(): Redis injects its prefix env var and is
// not seeded a backup config.
func TestProvisioner_RedisSkipsBackupAndInjectsPrefix(t *testing.T) {
	st := newFakeProvStore()
	eng := redisEngine(&fakeDocker{}, t.TempDir())
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()

	names := GeneratedNames{DBName: "cache_abc", RoleName: "cache_abc_u", Password: "pw"}
	row, _ := st.CreateManagedDatabase(context.Background(), "app1", "redis", names.DBName, &names.RoleName, []byte("sealed"), "REDIS_URL")

	p.provision(row, st.app, eng, names, []byte("sealed"))

	if st.statuses[row.ID] != "ready" {
		t.Errorf("status = %q, want ready", st.statuses[row.ID])
	}
	if _, ok := st.envVars["REDIS_URL"]; !ok {
		t.Errorf("connection string env var not injected")
	}
	if _, ok := st.envVars["REDIS_PREFIX"]; !ok {
		t.Errorf("key-prefix env var not injected")
	}
	if st.backupConfigs != 0 {
		t.Errorf("backup configs seeded = %d, want 0 (redis opts out)", st.backupConfigs)
	}
}
