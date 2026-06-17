//go:build integration

package dockercli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

// startAlpine boots a long-lived alpine container we can `docker exec` into.
func startAlpine(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      "alpine:3",
			Cmd:        []string{"sleep", "300"},
			WaitingFor: wait.ForExec([]string{"true"}).WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	id, err := c.GetContainerID(), error(nil)
	if err != nil || id == "" {
		t.Fatalf("container id: %v", err)
	}
	return id
}

func TestExec_CapturesStdout(t *testing.T) {
	id := startAlpine(t)
	var buf bytes.Buffer
	c := dockercli.New("")
	if err := c.Exec(context.Background(), id, []string{"echo hello-from-exec"}, &buf); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "hello-from-exec" {
		t.Errorf("stdout = %q, want hello-from-exec", got)
	}
}

func TestExecStdin_StreamsStdinIn(t *testing.T) {
	id := startAlpine(t)
	c := dockercli.New("")
	// `cat > /tmp/x` consumes stdin; read it back with Exec to prove the bytes
	// arrived — the round-trip the restore path relies on.
	in := strings.NewReader("dump-bytes-123")
	if err := c.ExecStdin(context.Background(), id, []string{"cat > /tmp/restore-test"}, in); err != nil {
		t.Fatalf("ExecStdin: %v", err)
	}
	var buf bytes.Buffer
	if err := c.Exec(context.Background(), id, []string{"cat /tmp/restore-test"}, &buf); err != nil {
		t.Fatalf("Exec readback: %v", err)
	}
	if got := buf.String(); got != "dump-bytes-123" {
		t.Errorf("stdin round-trip = %q, want dump-bytes-123", got)
	}
}

func TestExecStdin_NonZeroExitIsError(t *testing.T) {
	id := startAlpine(t)
	c := dockercli.New("")
	err := c.ExecStdin(context.Background(), id, []string{"echo boom 1>&2; exit 5"}, strings.NewReader(""))
	if err == nil {
		t.Fatal("ExecStdin: expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should surface stderr, got: %v", err)
	}
}

func TestExec_NonZeroExitIsError(t *testing.T) {
	id := startAlpine(t)
	var buf bytes.Buffer
	c := dockercli.New("")
	err := c.Exec(context.Background(), id, []string{"echo oops 1>&2; exit 7"}, &buf)
	if err == nil {
		t.Fatal("Exec: expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Errorf("error should surface stderr, got: %v", err)
	}
}
