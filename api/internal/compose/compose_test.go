package compose_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/compose"
)

func TestDetect_PrefersComposeYaml(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	mustWrite(t, filepath.Join(d, "compose.yaml"), "services: {}\n")
	mustWrite(t, filepath.Join(d, "docker-compose.yml"), "services: {}\n")
	res, err := compose.Detect(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != compose.SourceComposeYAML {
		t.Errorf("source = %s, want compose.yaml", res.Source)
	}
}

func TestDetect_FallsBackToDockerCompose(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	mustWrite(t, filepath.Join(d, "docker-compose.yml"), "services: {}\n")
	res, err := compose.Detect(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != compose.SourceDockerComposeYML {
		t.Errorf("source = %s, want docker-compose.yml", res.Source)
	}
}

func TestDetect_DockerfileOnlyReturnsGenerated(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	mustWrite(t, filepath.Join(d, "Dockerfile"), "FROM alpine\n")
	res, err := compose.Detect(d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != compose.SourceGenerated || res.Path != "" {
		t.Errorf("expected generated/empty path, got %+v", res)
	}
}

func TestDetect_EmptyRepoReturnsErrNoComposeOrDockerfile(t *testing.T) {
	t.Parallel()
	_, err := compose.Detect(t.TempDir())
	if !errors.Is(err, compose.ErrNoComposeOrDockerfile) {
		t.Errorf("err = %v, want ErrNoComposeOrDockerfile", err)
	}
}

func TestWrap_WritesGeneratedFile(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "vac-gen", "compose.yaml")
	path, err := compose.Wrap(dest)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The generated file must declare exactly one service called "app".
	svcs, err := compose.Parse(path)
	if err != nil {
		t.Fatalf("Parse generated: %v", err)
	}
	if len(svcs) != 1 || svcs[0].Name != "app" || !svcs[0].HasBuild {
		t.Errorf("generated services = %+v", svcs)
	}
	if !strings.Contains(string(body), "build: .") {
		t.Errorf("generated body missing build directive:\n%s", body)
	}
}

func TestWriteResourceOverride(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	path := filepath.Join(d, "compose.yaml")
	mustWrite(t, path, "services:\n  web:\n    image: nginx\n  worker:\n    image: busybox\n")

	ovr, err := compose.WriteResourceOverride(path, 256)
	if err != nil {
		t.Fatal(err)
	}
	if ovr == "" {
		t.Fatal("expected an override path, got empty")
	}
	body, err := os.ReadFile(ovr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"web:", "worker:", "mem_limit: 256m"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("override missing %q:\n%s", want, body)
		}
	}
	// The override must itself be a valid compose fragment naming the same
	// services, so the `-f` merge lines up.
	svcs, err := compose.Parse(ovr)
	if err != nil {
		t.Fatalf("parse override: %v", err)
	}
	if len(svcs) != 2 {
		t.Errorf("override services = %+v, want 2", svcs)
	}
}

func TestWriteResourceOverride_NoLimitWritesNothing(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	path := filepath.Join(d, "compose.yaml")
	mustWrite(t, path, "services:\n  web:\n    image: nginx\n")
	for _, limit := range []int{0, -5} {
		ovr, err := compose.WriteResourceOverride(path, limit)
		if err != nil || ovr != "" {
			t.Errorf("limit %d: got (%q, %v), want (\"\", nil)", limit, ovr, err)
		}
	}
}

func TestParse_MultiServiceComposeFile(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	path := filepath.Join(d, "compose.yaml")
	mustWrite(t, path, `
services:
  web:
    build: .
    ports:
      - "8080:80"
      - 9090
  worker:
    image: ghcr.io/example/worker:1.2.3
    ports:
      - "127.0.0.1:5005:5000"
      - published: 6000
        target: 6000
  db:
    image: postgres:16
`)
	svcs, err := compose.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"db", "web", "worker"}
	gotNames := make([]string, len(svcs))
	for i, s := range svcs {
		gotNames[i] = s.Name
	}
	if !reflect.DeepEqual(wantNames, gotNames) {
		t.Errorf("names = %v, want %v", gotNames, wantNames)
	}

	byName := map[string]compose.Service{}
	for _, s := range svcs {
		byName[s.Name] = s
	}
	if !byName["web"].HasBuild {
		t.Error("web should have HasBuild")
	}
	if byName["worker"].Image != "ghcr.io/example/worker:1.2.3" {
		t.Errorf("worker image = %s", byName["worker"].Image)
	}
	wantWorkerPorts := []int{5005, 6000}
	if !reflect.DeepEqual(byName["worker"].Ports, wantWorkerPorts) {
		t.Errorf("worker ports = %v, want %v", byName["worker"].Ports, wantWorkerPorts)
	}
	wantWebPorts := []int{8080, 9090}
	if !reflect.DeepEqual(byName["web"].Ports, wantWebPorts) {
		t.Errorf("web ports = %v, want %v", byName["web"].Ports, wantWebPorts)
	}
}

func TestParse_MissingServicesSection(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	path := filepath.Join(d, "compose.yaml")
	mustWrite(t, path, "version: '3'\n")
	if _, err := compose.Parse(path); err == nil {
		t.Errorf("expected error parsing file with no services section")
	}
}

func TestWarnIfMissingDockerignore(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	if compose.WarnIfMissingDockerignore(d) == "" {
		t.Error("expected warning for repo with no .dockerignore")
	}
	mustWrite(t, filepath.Join(d, ".dockerignore"), "node_modules/\n")
	if compose.WarnIfMissingDockerignore(d) != "" {
		t.Error("expected no warning when .dockerignore exists")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
