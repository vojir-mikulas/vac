package dockercli

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// LogLine is one line of container output, tagged by which stream it came from.
type LogLine struct {
	Stream  string // "stdout" | "stderr"
	Message string
}

// Logs follows a container's stdout/stderr via `docker logs --follow`. The
// returned channel is closed when ctx is cancelled or the container's log
// stream ends (container removed). `since` bounds the backlog: pass the
// follower's start time so a long-lived container isn't re-ingested from the
// beginning on every (re)attach.
//
// stdout and stderr are read as separate pipes so each line is correctly
// classified — `docker logs` writes container stdout to its stdout and stderr
// to its stderr (outside a TTY).
func (c *Compose) Logs(ctx context.Context, containerID string, since time.Time) (<-chan LogLine, error) {
	args := []string{"logs", "--follow", "--since", since.UTC().Format(time.RFC3339), containerID}
	cmd := c.command(ctx, "", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrDockerMissing
		}
		return nil, err
	}

	out := make(chan LogLine, 64)
	var wg sync.WaitGroup
	wg.Add(2)
	scan := func(r interface{ Read([]byte) (int, error) }, stream string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case out <- LogLine{Stream: stream, Message: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
	}
	go scan(stdout, LogStreamStdout)
	go scan(stderr, LogStreamStderr)
	go func() {
		wg.Wait()
		_ = cmd.Wait()
		close(out)
	}()
	return out, nil
}

// Log stream tags, mirroring store.RuntimeLogStream* without the import.
const (
	LogStreamStdout = "stdout"
	LogStreamStderr = "stderr"
)
