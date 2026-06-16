package dockercli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
)

// Events subscribes to the host daemon's event stream filtered to container
// events. The returned channel is closed when ctx is cancelled or the
// underlying `docker events` process exits. Decode errors are logged into
// the channel as zero-value Events with Action="vac:parse-error".
//
// We open a long-running `docker events --format '{{json .}}' --filter
// type=container` and parse line-by-line.
func (c *Compose) Events(ctx context.Context) (<-chan Event, error) {
	cmd := c.command(ctx, "", "events", "--format", "{{json .}}", "--filter", "type=container")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrDockerMissing
		}
		return nil, err
	}
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		// Reap the child on EVERY exit path. Without this, the early `return` on
		// ctx.Done() below (out-channel blocked at shutdown) would leave a zombie
		// `docker events` process and leaked stdout FD per monitor restart.
		// docker events exit is usually ctx cancellation (clean shutdown) or the
		// daemon going away; log the latter at debug.
		defer func() {
			if err := cmd.Wait(); err != nil && ctx.Err() == nil {
				slog.Debug("dockercli: events stream exited", "err", err)
			}
		}()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			var ev Event
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				// Skip mal-formed lines silently — we don't want a single
				// noisy event to kill the monitor.
				continue
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// RestartContainers runs `docker restart <name>...` against raw containers (not
// compose projects). Used by the instance-level control-plane restart, which
// bounces the vac-* infrastructure containers by name. A missing/unknown name
// surfaces as an error.
func (c *Compose) RestartContainers(ctx context.Context, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"restart"}, names...)
	cmd := c.command(ctx, "", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// Inspect runs `docker inspect <id>` and returns the raw JSON. Callers
// decode the bits they need (the full ContainerJSON shape is hundreds of
// fields — not worth modelling).
func (c *Compose) Inspect(ctx context.Context, containerID string) ([]byte, error) {
	cmd := c.command(ctx, "", "inspect", containerID)
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	return out, nil
}

// ListImages returns images filtered by compose project + service labels.
// Used by the image-prune step to keep only the most recent N per service.
func (c *Compose) ListImages(ctx context.Context, projectName, serviceName string) ([]Image, error) {
	args := []string{
		"images",
		"--format", "{{json .}}",
		"--filter", "label=com.docker.compose.project=" + projectName,
	}
	if serviceName != "" {
		args = append(args, "--filter", "label=com.docker.compose.service="+serviceName)
	}
	cmd := c.command(ctx, "", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	return parseImagesOutput(out)
}

// RemoveImage runs `docker image rm <id>`. Failure on "image is in use" is
// expected and surfaced to the caller; the pruner ignores those.
func (c *Compose) RemoveImage(ctx context.Context, id string) error {
	cmd := c.command(ctx, "", "image", "rm", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// BuildCachePrune bounds the BuildKit layer cache to maxBytes, deleting the
// least-recently-used cache records beyond that ceiling. It targets the default
// builder (the one `docker compose build` uses) — no custom builder instance is
// required. Best-effort, like RemoveImage: callers (the nightly pruner) swallow
// failures.
//
// `--keep-storage` is the long-standing flag but is deprecated in newer buildx
// (≥0.17, which renamed it `--reserved-space`) and absent from the older
// `docker builder prune` shim. We try `buildx prune` first and, if that fails in
// a way that looks like a flag/parse error, fall back to `builder prune` so the
// pass works across CLI versions.
func (c *Compose) BuildCachePrune(ctx context.Context, maxBytes int64) error {
	if maxBytes < 0 {
		maxBytes = 0
	}
	keep := strconv.FormatInt(maxBytes, 10)
	cmd := c.command(ctx, "", "buildx", "prune", "--force", "--keep-storage", keep)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// buildx unavailable or the flag was rejected — retry via the classic
	// `docker builder prune`, which still accepts --keep-storage on older CLIs.
	fb := c.command(ctx, "", "builder", "prune", "--force", "--keep-storage", keep)
	if _, ferr := fb.CombinedOutput(); ferr == nil {
		return nil
	}
	return mapCmdError(err, out)
}

func parseImagesOutput(b []byte) ([]Image, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out []Image
	scanner := bufio.NewScanner(strings.NewReader(string(b)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var img Image
		if err := json.Unmarshal([]byte(line), &img); err != nil {
			return nil, fmt.Errorf("dockercli: parse image line: %w", err)
		}
		out = append(out, img)
	}
	return out, scanner.Err()
}
