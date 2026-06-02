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
