package dockercli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
		_ = cmd.Wait()
	}()
	return out, nil
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
