package dbprovision

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

// DockerController is the slice of *dockercli.Compose the shared engines use to
// lazily start their daemon and provision into it.
type DockerController interface {
	Up(ctx context.Context, projectDir, composeFile, projectName, envFile string, overrideFiles ...string) error
	Ps(ctx context.Context, projectName string) ([]dockercli.PsService, error)
	Exec(ctx context.Context, containerID string, cmd []string, out io.Writer) error
}

// writeComposeProject materializes a shared engine's compose file under
// {workDir}/managed/{engine}/compose.yaml and returns the project directory.
func writeComposeProject(workDir, engine, composeYAML string) (string, error) {
	dir := filepath.Join(workDir, "managed", engine)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(composeYAML), 0o640); err != nil {
		return "", err
	}
	return dir, nil
}

// execOK runs a command in a container and reports only success/failure
// (discarding stdout) — used for provisioning DDL and readiness pings.
func execOK(ctx context.Context, docker DockerController, container string, command string) error {
	return docker.Exec(ctx, container, []string{command}, io.Discard)
}

// pingUntilReady polls an exec command until it succeeds or the deadline passes.
// A freshly-started database daemon refuses connections for a few seconds, so
// the first attempts are expected to fail.
func pingUntilReady(ctx context.Context, docker DockerController, container, pingCommand string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := execOK(ctx, docker, container, pingCommand); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
