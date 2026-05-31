// Package adapter turns a cloned repo into a compose file VAC can build & up,
// regardless of how the operator authored their app. Every adapter ultimately
// produces a compose file — the deploy pipeline stays compose-driven, so all
// architecture invariants (vac-edge routing, Caddy health-gating) hold no
// matter which build source the user picked.
//
// Detection order when build_kind = "auto": compose → dockerfile → framework →
// (none). The first that matches the repo wins.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/compose"
)

// Build kinds. These are the values stored in apps.build_kind.
const (
	KindAuto       = "auto"
	KindCompose    = "compose"
	KindDockerfile = "dockerfile"
	KindFramework  = "framework"
	KindStatic     = "static"
)

// ErrUndetected is returned when build_kind is "auto" but the repo matches no
// adapter — the pipeline surfaces this as a clear "configure your build" error.
var ErrUndetected = errors.New("adapter: could not auto-detect a build type (no compose file, Dockerfile, or known framework)")

// BuildConfig is the adapter-specific configuration persisted as
// apps.build_config (JSONB). Only the fields relevant to the selected kind are
// populated; the rest stay zero.
type BuildConfig struct {
	// compose
	ComposePath string `json:"composePath,omitempty"`
	// dockerfile
	DockerfilePath string `json:"dockerfilePath,omitempty"`
	// framework
	Framework    string `json:"framework,omitempty"`
	BuildCommand string `json:"buildCommand,omitempty"`
	StartCommand string `json:"startCommand,omitempty"`
	Port         int    `json:"port,omitempty"`
	// static
	StaticDir   string `json:"staticDir,omitempty"`
	SPAFallback bool   `json:"spaFallback,omitempty"`
}

// ParseConfig unmarshals stored build_config JSON. Empty/nil → zero config.
func ParseConfig(raw json.RawMessage) (BuildConfig, error) {
	var cfg BuildConfig
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("adapter: invalid build_config: %w", err)
	}
	return cfg, nil
}

// Adapter resolves/produces the compose file for one build kind.
type Adapter interface {
	Kind() string
	// Prepare resolves or synthesizes a compose file inside repoDir that VAC
	// can build & up, returning its absolute path. Non-compose adapters write
	// a generated wrapper (and any helper files) into repoDir — never into the
	// repo's tracked tree in a way the next pull can't reset.
	Prepare(ctx context.Context, repoDir string, cfg BuildConfig) (composePath string, err error)
}

// For returns the adapter for kind. When kind is "" or "auto" it runs Detect
// against repoDir and uses the result (ErrUndetected when nothing matches).
func For(kind, repoDir string) (Adapter, error) {
	if kind == "" || kind == KindAuto {
		kind = Detect(repoDir)
	}
	switch kind {
	case KindCompose:
		return composeAdapter{}, nil
	case KindDockerfile:
		return dockerfileAdapter{}, nil
	case KindStatic:
		return staticAdapter{}, nil
	case KindFramework:
		return frameworkAdapter{}, nil
	default:
		return nil, ErrUndetected
	}
}

// Detect implements the auto-detection order: compose → dockerfile → framework
// → "". It never errors — an empty string means "nothing matched".
func Detect(repoDir string) string {
	res, err := compose.Detect(repoDir)
	if err == nil {
		if res.Source == compose.SourceGenerated {
			return KindDockerfile // only a Dockerfile present
		}
		return KindCompose
	}
	if detectFramework(repoDir) != "" {
		return KindFramework
	}
	return ""
}

// Validate checks that a (kind, cfg) pair is internally consistent before it's
// persisted. It does not touch the filesystem — repo-relative paths are
// re-validated against the cloned tree at Prepare time.
func Validate(kind string, cfg BuildConfig) error {
	switch kind {
	case KindAuto, KindCompose, KindDockerfile, KindStatic:
		// All knobs optional; path safety is enforced at Prepare time.
		return nil
	case KindFramework:
		if strings.TrimSpace(cfg.Framework) == "" {
			return errors.New("adapter: framework build requires a framework")
		}
		if cfg.Port < 0 || cfg.Port > 65535 {
			return errors.New("adapter: port must be 0..65535")
		}
		return nil
	default:
		return fmt.Errorf("adapter: unknown build_kind %q", kind)
	}
}

// safeRepoPath joins rel onto repoDir, rejecting absolute paths and any path
// that escapes the repo. Returns the cleaned absolute path.
func safeRepoPath(repoDir, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("adapter: path %q must be relative to the repo", rel)
	}
	full := filepath.Join(repoDir, clean)
	r, err := filepath.Rel(repoDir, full)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("adapter: path %q escapes the repo", rel)
	}
	return full, nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// composeWrapPath is where generated adapters write their synthesized compose
// file inside the repo working tree. The wrapper's `build`/volume paths are
// relative to it (the repo root), and a later pull's `reset --hard` leaves
// untracked files in place — but we rewrite it every deploy regardless.
func composeWrapPath(repoDir string) string {
	return filepath.Join(repoDir, "compose.yaml")
}
