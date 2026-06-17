package adapter_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
)

func TestImageAdapter_GeneratesCompose(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, err := adapter.For(adapter.KindImage, d)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{
		Image: "ghcr.io/me/app:1.4.2",
		Port:  8080,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads the file it just wrote
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"image: ghcr.io/me/app:1.4.2", "expose:", `- "8080"`, "env_file:"} {
		if !strings.Contains(got, want) {
			t.Errorf("compose missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "build:") {
		t.Errorf("compose must not declare build:\n%s", got)
	}
	if strings.Contains(got, "ports:") {
		t.Errorf("compose must not publish host ports:\n%s", got)
	}
}

func TestImageAdapter_PortlessWorkerHasNoExpose(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindImage, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{Image: "redis:7"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	b, _ := os.ReadFile(path) //nolint:gosec // test reads the file it just wrote
	if strings.Contains(string(b), "expose:") {
		t.Errorf("port-less image must not expose a port:\n%s", b)
	}
}

func TestImageAdapter_RejectsBadRef(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindImage, d)
	for _, bad := range []string{"", "  ", "ghcr.io/me/app; rm -rf /", "has space"} {
		if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{Image: bad}); err == nil {
			t.Errorf("Prepare(%q): expected error", bad)
		}
	}
}

func TestValidate_Image(t *testing.T) {
	t.Parallel()
	if err := adapter.Validate(adapter.KindImage, adapter.BuildConfig{Image: "nginx:alpine"}); err != nil {
		t.Errorf("valid image rejected: %v", err)
	}
	if err := adapter.Validate(adapter.KindImage, adapter.BuildConfig{}); err == nil {
		t.Error("empty image accepted")
	}
	if err := adapter.Validate(adapter.KindImage, adapter.BuildConfig{Image: "nginx", Port: 70000}); err == nil {
		t.Error("out-of-range port accepted")
	}
}
