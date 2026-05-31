package compose_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/compose"
)

// Each of the four supported filenames must be detected on its own.
func TestDetect_AllSupportedFilenames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file string
		want compose.Source
	}{
		{"compose.yaml", compose.SourceComposeYAML},
		{"compose.yml", compose.SourceComposeYML},
		{"docker-compose.yml", compose.SourceDockerComposeYML},
		{"docker-compose.yaml", compose.SourceDockerComposeYAML},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			d := t.TempDir()
			mustWrite(t, filepath.Join(d, tc.file), "services: {}\n")
			res, err := compose.Detect(d)
			if err != nil {
				t.Fatal(err)
			}
			if res.Source != tc.want {
				t.Errorf("source = %s, want %s", res.Source, tc.want)
			}
			if res.Path != filepath.Join(d, tc.file) {
				t.Errorf("path = %s, want %s", res.Path, filepath.Join(d, tc.file))
			}
		})
	}
}

// Priority order: compose.yml beats docker-compose.* when both present.
func TestDetect_PriorityOrder(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	mustWrite(t, filepath.Join(d, "compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(d, "docker-compose.yaml"), "services: {}\n")
	res, err := compose.Detect(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != compose.SourceComposeYML {
		t.Errorf("source = %s, want compose.yml", res.Source)
	}
}

// An empty / default configured path falls back to auto-detection.
func TestDetectAt_DefaultFallsBackToAuto(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	mustWrite(t, filepath.Join(d, "compose.yml"), "services: {}\n")
	for _, configured := range []string{"", "compose.yaml", "  "} {
		res, err := compose.DetectAt(d, configured)
		if err != nil {
			t.Fatalf("configured=%q: %v", configured, err)
		}
		if res.Source != compose.SourceComposeYML {
			t.Errorf("configured=%q: source = %s, want compose.yml", configured, res.Source)
		}
	}
}

// An explicit, non-default path is used directly when it exists.
func TestDetectAt_ConfiguredPathHonoured(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(d, "deploy", "stack.yaml"), "services: {}\n")
	res, err := compose.DetectAt(d, "deploy/stack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != compose.SourceConfigured {
		t.Errorf("source = %s, want configured", res.Source)
	}
	if res.Path != filepath.Join(d, "deploy", "stack.yaml") {
		t.Errorf("path = %s", res.Path)
	}
}

// A configured path that does not exist is a clear error (not a silent
// fallback to compose.yaml / Dockerfile).
func TestDetectAt_ConfiguredPathMissing(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	mustWrite(t, filepath.Join(d, "compose.yaml"), "services: {}\n")
	_, err := compose.DetectAt(d, "does-not-exist.yaml")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not-found error", err)
	}
}

// A configured path that escapes the repo is rejected.
func TestDetectAt_ConfiguredPathEscape(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	for _, escape := range []string{"../evil.yaml", "/etc/passwd", "a/../../evil.yaml"} {
		if _, err := compose.DetectAt(d, escape); err == nil {
			t.Errorf("path %q: expected escape error, got nil", escape)
		}
	}
}
