// Package compose detects, parses, and (when only a Dockerfile is present)
// auto-generates the Docker Compose file VAC uses for `docker compose build`
// / `up`. Repo shape is examined; nothing is mutated inside the repo.
package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source identifies which file was selected (or "generated" when VAC wrote
// a wrapper).
type Source string

const (
	SourceComposeYAML       Source = "compose.yaml"
	SourceComposeYML        Source = "compose.yml"
	SourceDockerComposeYML  Source = "docker-compose.yml"
	SourceDockerComposeYAML Source = "docker-compose.yaml"
	// SourceConfigured marks a compose file the operator explicitly pointed
	// VAC at via App.ComposeFile (rather than one found by auto-detection).
	SourceConfigured Source = "configured"
	SourceGenerated  Source = "generated"
)

// composeCandidates is the auto-detection priority order. The first file that
// exists in the repo wins. Covers both `compose.*` (current spelling) and the
// legacy `docker-compose.*` names, in both `.yml` and `.yaml` extensions.
var composeCandidates = []Source{
	SourceComposeYAML,
	SourceComposeYML,
	SourceDockerComposeYML,
	SourceDockerComposeYAML,
}

// Result is the outcome of Detect: which kind of compose file to use, plus
// the absolute path to it (for "generated" the caller fills Path after
// calling Wrap).
type Result struct {
	Source Source
	Path   string
}

// ErrNoComposeOrDockerfile is returned when a repo has neither compose nor a
// Dockerfile; the pipeline maps this to deployment status `error` with a
// clear user-facing message.
var ErrNoComposeOrDockerfile = errors.New("compose: repo has no compose.yaml, compose.yml, docker-compose.yml, docker-compose.yaml, or Dockerfile")

// defaultComposeFile is the per-app default stored on App.ComposeFile. When
// the configured path equals this, DetectAt falls back to auto-detection so a
// repo using e.g. compose.yml or only a Dockerfile still deploys.
const defaultComposeFile = "compose.yaml"

// DetectAt resolves the compose file for a repo, honouring an explicitly
// configured path when the operator has set one. configuredPath is the
// per-app App.ComposeFile value; an empty string or the default
// ("compose.yaml") means "auto-detect" (see Detect).
//
// When an explicit, non-default path is given it is resolved relative to
// repoDir and must exist — a missing or escaping path is a hard error so the
// operator gets a clear message instead of a silent fallback to the wrong file.
func DetectAt(repoDir, configuredPath string) (Result, error) {
	p := strings.TrimSpace(configuredPath)
	if p == "" || p == defaultComposeFile {
		return Detect(repoDir)
	}
	return detectConfigured(repoDir, p)
}

// detectConfigured resolves an explicit compose path inside repoDir, rejecting
// anything that would escape the repo working tree.
func detectConfigured(repoDir, configuredPath string) (Result, error) {
	clean := filepath.Clean(configuredPath)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return Result{}, fmt.Errorf("compose: configured compose file %q must be a relative path inside the repo", configuredPath)
	}
	full := filepath.Join(repoDir, clean)
	// filepath.Join cleans the result; verify it still lives under repoDir.
	rel, err := filepath.Rel(repoDir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Result{}, fmt.Errorf("compose: configured compose file %q escapes the repo", configuredPath)
	}
	info, statErr := os.Stat(full)
	if statErr != nil || info.IsDir() {
		return Result{}, fmt.Errorf("compose: configured compose file %q not found in repo", configuredPath)
	}
	return Result{Source: SourceConfigured, Path: full}, nil
}

// Detect inspects the repo working tree and decides which compose file to
// drive. Detection order matches composeCandidates, then a Dockerfile-only
// repo gets an auto-generated wrapper.
func Detect(repoDir string) (Result, error) {
	for _, name := range composeCandidates {
		if p := exists(repoDir, string(name)); p != "" {
			return Result{Source: name, Path: p}, nil
		}
	}
	if exists(repoDir, "Dockerfile") != "" {
		// Caller wraps separately via Wrap() — VAC never writes into the
		// repo working tree.
		return Result{Source: SourceGenerated}, nil
	}
	return Result{}, ErrNoComposeOrDockerfile
}

func exists(dir, name string) string {
	p := filepath.Join(dir, name)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}
