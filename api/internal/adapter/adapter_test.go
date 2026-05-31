package adapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect_Order(t *testing.T) {
	t.Parallel()
	t.Run("compose wins", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "compose.yaml", "services: {}\n")
		write(t, d, "Dockerfile", "FROM alpine\n")
		if got := adapter.Detect(d); got != adapter.KindCompose {
			t.Errorf("got %q, want compose", got)
		}
	})
	t.Run("dockerfile only", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "Dockerfile", "FROM alpine\n")
		if got := adapter.Detect(d); got != adapter.KindDockerfile {
			t.Errorf("got %q, want dockerfile", got)
		}
	})
	t.Run("framework react", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "package.json", `{"dependencies":{"react":"^19.0.0"}}`)
		if got := adapter.Detect(d); got != adapter.KindFramework {
			t.Errorf("got %q, want framework", got)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		t.Parallel()
		if got := adapter.Detect(t.TempDir()); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestFor_AutoResolvesAndUndetected(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "Dockerfile", "FROM alpine\n")
	ad, err := adapter.For(adapter.KindAuto, d)
	if err != nil {
		t.Fatal(err)
	}
	if ad.Kind() != adapter.KindDockerfile {
		t.Errorf("kind = %q, want dockerfile", ad.Kind())
	}

	if _, err := adapter.For(adapter.KindAuto, t.TempDir()); err == nil {
		t.Error("expected ErrUndetected for empty repo")
	}
}

func TestComposeAdapter_Prepare(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "compose.yml", "services: {}\n")
	ad, _ := adapter.For(adapter.KindCompose, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(d, "compose.yml") {
		t.Errorf("path = %s", path)
	}
}

func TestDockerfileAdapter_Prepare_CustomPath(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "docker/Api.Dockerfile", "FROM alpine\n")
	ad, _ := adapter.For(adapter.KindDockerfile, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{DockerfilePath: "docker/Api.Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "docker/Api.Dockerfile") {
		t.Errorf("generated compose missing dockerfile path:\n%s", body)
	}
	if !strings.Contains(string(body), "context: .") {
		t.Errorf("generated compose missing build context:\n%s", body)
	}
}

func TestDockerfileAdapter_Prepare_Missing(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindDockerfile, d)
	if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{DockerfilePath: "nope.Dockerfile"}); err == nil {
		t.Error("expected error for missing Dockerfile")
	}
}

func TestStaticAdapter_Prepare_SPAFallback(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "public/index.html", "<html></html>")
	ad, _ := adapter.For(adapter.KindStatic, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{StaticDir: "public", SPAFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(path)
	if !strings.Contains(string(compose), "nginx:alpine") || !strings.Contains(string(compose), "./public:/usr/share/nginx/html") {
		t.Errorf("compose missing nginx/static mount:\n%s", compose)
	}
	conf, err := os.ReadFile(filepath.Join(d, "vac-static.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "/index.html") {
		t.Errorf("spa fallback conf missing index.html:\n%s", conf)
	}
}

func TestStaticAdapter_Prepare_No404Fallback(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "index.html", "<html></html>")
	ad, _ := adapter.For(adapter.KindStatic, d)
	if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{}); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(d, "vac-static.conf"))
	if !strings.Contains(string(conf), "=404") {
		t.Errorf("non-spa conf should 404 unknown paths:\n%s", conf)
	}
}

func TestFrameworkAdapter_Prepare_React(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindFramework, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{Framework: "React", BuildCommand: "pnpm i && pnpm build"})
	if err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(path)
	if !strings.Contains(string(compose), "Dockerfile.vac") {
		t.Errorf("compose missing generated dockerfile ref:\n%s", compose)
	}
	df, err := os.ReadFile(filepath.Join(d, "Dockerfile.vac"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "pnpm i && pnpm build") || !strings.Contains(string(df), "nginx:alpine") {
		t.Errorf("generated Dockerfile wrong:\n%s", df)
	}
}

func TestFrameworkAdapter_Prepare_Unsupported(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindFramework, d)
	if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{Framework: "svelte"}); err == nil {
		t.Error("expected unsupported-framework error")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	if err := adapter.Validate(adapter.KindFramework, adapter.BuildConfig{}); err == nil {
		t.Error("framework without framework field should fail validation")
	}
	if err := adapter.Validate(adapter.KindFramework, adapter.BuildConfig{Framework: "react"}); err != nil {
		t.Errorf("valid framework config rejected: %v", err)
	}
	if err := adapter.Validate("bogus", adapter.BuildConfig{}); err == nil {
		t.Error("unknown kind should fail validation")
	}
}

func TestParseConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	cfg, err := adapter.ParseConfig(json.RawMessage(`{"composePath":"deploy/stack.yaml","port":3000}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposePath != "deploy/stack.yaml" || cfg.Port != 3000 {
		t.Errorf("round-trip mismatch: %+v", cfg)
	}
	if _, err := adapter.ParseConfig(nil); err != nil {
		t.Errorf("nil config should parse to zero value: %v", err)
	}
}
