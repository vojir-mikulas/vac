package dockercli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StatSample is one container's row from `docker stats --no-stream`. The fields
// are the human-formatted strings docker emits; the stats package parses them
// into typed numbers.
type StatSample struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
	PIDs     string `json:"PIDs"`
}

// Stats runs one `docker stats --no-stream` poll for the given container ids
// and returns a sample per container. An empty id list is a no-op. The poll is
// bounded (it exits immediately) so the caller's ticker controls cadence.
func (c *Compose) Stats(ctx context.Context, ids []string) ([]StatSample, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, ids...)
	cmd := c.command(ctx, "", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	return parseStatsOutput(out)
}

func parseStatsOutput(b []byte) ([]StatSample, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var out []StatSample
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var s StatSample
		if err := json.Unmarshal(line, &s); err != nil {
			return nil, fmt.Errorf("dockercli: parse stats line: %w", err)
		}
		out = append(out, s)
	}
	return out, scanner.Err()
}

// ContainerStartedAt returns a container's start time via `docker inspect`. The
// stats collector caches this per container (it doesn't change) to compute
// uptime without re-inspecting on every poll.
func (c *Compose) ContainerStartedAt(ctx context.Context, id string) (time.Time, error) {
	cmd := c.command(ctx, "", "inspect", "-f", "{{.State.StartedAt}}", id)
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, mapCmdError(err, out)
	}
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
}
