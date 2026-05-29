// Package compose detects, parses, and (when only a Dockerfile is present)
// auto-generates the Docker Compose file VAC uses for `docker compose build`
// / `up`. Repo shape is examined; nothing is mutated inside the repo.
package compose

import (
	"errors"
	"os"
	"path/filepath"
)

// Source identifies which file was selected (or "generated" when VAC wrote
// a wrapper).
type Source string

const (
	SourceComposeYAML       Source = "compose.yaml"
	SourceDockerComposeYAML Source = "docker-compose.yml"
	SourceGenerated         Source = "generated"
)

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
var ErrNoComposeOrDockerfile = errors.New("compose: repo has no compose.yaml, docker-compose.yml, or Dockerfile")

// Detect inspects the repo working tree and decides which compose file to
// drive. Detection order matches mvp.md § Deployment Model.
func Detect(repoDir string) (Result, error) {
	if p := exists(repoDir, "compose.yaml"); p != "" {
		return Result{Source: SourceComposeYAML, Path: p}, nil
	}
	if p := exists(repoDir, "docker-compose.yml"); p != "" {
		return Result{Source: SourceDockerComposeYAML, Path: p}, nil
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
