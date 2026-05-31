package adapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/compose"
)

// dockerfileAdapter wraps a single Dockerfile (at cfg.DockerfilePath, default
// "Dockerfile") into a generated compose file. This formalizes the existing
// Dockerfile-only auto behaviour and adds an editable path.
type dockerfileAdapter struct{}

func (dockerfileAdapter) Kind() string { return KindDockerfile }

func (dockerfileAdapter) Prepare(_ context.Context, repoDir string, cfg BuildConfig) (string, error) {
	dockerfile := strings.TrimSpace(cfg.DockerfilePath)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	full, err := safeRepoPath(repoDir, dockerfile)
	if err != nil {
		return "", err
	}
	if !fileExists(full) {
		return "", fmt.Errorf("adapter: Dockerfile %q not found in repo", dockerfile)
	}
	return compose.WrapDockerfile(composeWrapPath(repoDir), dockerfile)
}
