// Package reqmetrics turns Caddy's JSON access log into a per-service
// request-rate series. Caddy's Prometheus metrics aren't labelled by request
// host, so they can't attribute a request to a service — the access log can.
//
// The tailer is a hand-rolled poller (no external tail dependency): it reopens
// the file each tick, reads from the last offset, and resets on truncation.
// Good enough for 10s-bucket aggregation; access-log rotation is rare.
package reqmetrics

import (
	"bufio"
	"context"
	"io"
	"os"
	"time"
)

// Tail follows the file at path, calling handle for each complete line, until
// ctx is cancelled. Missing files are tolerated (it waits for the file to
// appear). poll controls how often the file is checked.
func Tail(ctx context.Context, path string, poll time.Duration, handle func(line []byte)) {
	if poll <= 0 {
		poll = time.Second
	}
	var offset int64
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		offset = drain(path, offset, handle)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// drain reads any bytes after offset and returns the new offset. On a shrunk
// file (truncation/rotation) it restarts from 0.
func drain(path string, offset int64, handle func([]byte)) int64 {
	f, err := os.Open(path) //nolint:gosec // path is operator-controlled config
	if err != nil {
		return offset
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return offset
	}
	if st.Size() < offset {
		offset = 0 // truncated or rotated
	}
	if st.Size() == offset {
		return offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			handle(line)
			offset += int64(len(line))
		}
		if err != nil {
			break // EOF or partial trailing line — re-read it next tick
		}
	}
	return offset
}
