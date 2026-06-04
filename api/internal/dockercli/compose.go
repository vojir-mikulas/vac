package dockercli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ErrDockerMissing is returned when the docker binary isn't on PATH.
var ErrDockerMissing = errors.New("dockercli: docker binary not on PATH")

// Compose wraps `docker compose ...` invocations against a specific project.
// The empty zero value uses no extra env and `docker` from PATH.
type Compose struct {
	// Socket overrides DOCKER_HOST. Empty = default unix:///var/run/docker.sock.
	Socket string
	// ExtraEnv lines added to every invocation, in addition to PATH/HOME.
	// Empty by default — callers like the pipeline pass DOCKER_BUILDKIT=1.
	ExtraEnv []string
}

// New returns a Compose wired to the given docker socket. Passing "" uses
// the daemon's default.
func New(socket string) *Compose { return &Compose{Socket: socket} }

// Build runs `docker compose build` with BuildKit enabled. Output is
// streamed line-by-line into `out` so the deploy log writer can flush
// progressively. The compose project name distinguishes user stacks from
// VAC internals.
func (c *Compose) Build(ctx context.Context, projectDir, composeFile, projectName string, out io.Writer) error {
	// --progress is a GLOBAL compose flag and must precede the `build`
	// subcommand; placing it after build works but compose warns about it.
	args := []string{"compose", "--progress", "plain", "-p", projectName, "-f", composeFile, "build"}
	cmd := c.command(ctx, projectDir, args...)
	// BuildKit is the modern builder — Compose v2 picks it up from
	// DOCKER_BUILDKIT=1 the same way the standalone CLI does.
	cmd.Env = append(cmd.Env, "DOCKER_BUILDKIT=1")
	return runStreaming(cmd, out)
}

// Up runs `docker compose up -d --remove-orphans`. The caller is expected to
// have already written the .env file (via --env-file or co-located). Any
// overrideFiles are merged after the base compose file (each as an extra `-f`),
// in order — used to layer VAC's per-app resource limits over the user's compose
// without rewriting it.
func (c *Compose) Up(ctx context.Context, projectDir, composeFile, projectName string, envFile string, overrideFiles ...string) error {
	args := []string{"compose", "-p", projectName, "-f", composeFile}
	for _, f := range overrideFiles {
		if f != "" {
			args = append(args, "-f", f)
		}
	}
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	args = append(args, "up", "-d", "--remove-orphans")
	cmd := c.command(ctx, projectDir, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// Down stops and removes the project's containers + networks. Volumes are
// preserved unless removeVolumes is true (data loss — caller's call).
func (c *Compose) Down(ctx context.Context, projectName string, removeVolumes bool) error {
	args := []string{"compose", "-p", projectName, "down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "-v")
	}
	cmd := c.command(ctx, "", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// Stop stops services in the project. If `service` is empty, stops the
// whole stack. Used by the crash-loop monitor (single service) and the
// stack-control endpoints (whole stack).
func (c *Compose) Stop(ctx context.Context, projectName, service string) error {
	args := []string{"compose", "-p", projectName, "stop"}
	if service != "" {
		args = append(args, service)
	}
	cmd := c.command(ctx, "", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// Start starts previously-stopped services. Idempotent — already-running
// services are left alone.
func (c *Compose) Start(ctx context.Context, projectName, service string) error {
	args := []string{"compose", "-p", projectName, "start"}
	if service != "" {
		args = append(args, service)
	}
	cmd := c.command(ctx, "", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// Restart restarts services. Empty `service` restarts the whole stack.
func (c *Compose) Restart(ctx context.Context, projectName, service string) error {
	args := []string{"compose", "-p", projectName, "restart"}
	if service != "" {
		args = append(args, service)
	}
	cmd := c.command(ctx, "", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return mapCmdError(err, out)
	}
	return nil
}

// Config renders the fully resolved, merged compose document via `docker compose
// config` — applying include:/extends:/override files and variable interpolation
// exactly as `up` would. The deploy preflight lints THIS output rather than the
// raw file, so a malicious construct hidden behind an include can't slip past the
// host-escape checks. Returns the resolved YAML on stdout; compose warnings go to
// stderr and are not captured.
func (c *Compose) Config(ctx context.Context, projectDir, composeFile, projectName string) ([]byte, error) {
	cmd := c.command(ctx, projectDir, "compose", "-p", projectName, "-f", composeFile, "config")
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	return out, nil
}

// Ps lists services in the project. Compose v2 outputs either a JSON array
// or one JSON object per line depending on version; we accept both.
func (c *Compose) Ps(ctx context.Context, projectName string) ([]PsService, error) {
	cmd := c.command(ctx, "", "compose", "-p", projectName, "ps", "--all", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	return ParsePsOutput(out)
}

// ParsePsOutput tolerates both `[{},{}]` and `\n`-delimited streams.
// Exported so tests can pin the parsing behaviour directly.
func ParsePsOutput(b []byte) ([]PsService, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var out []PsService
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, fmt.Errorf("dockercli: parse ps array: %w", err)
		}
		return out, nil
	}
	// Line-delimited fallback (older compose versions).
	var out []PsService
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var p PsService
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("dockercli: parse ps line: %w", err)
		}
		out = append(out, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dockercli: scan ps: %w", err)
	}
	return out, nil
}

// Exec runs `docker exec {containerID} sh -c {cmd}` and streams the command's
// stdout into `out` (Track D / D1). It runs inside the *running* container so
// engine credentials already present in the container env (e.g. $POSTGRES_USER)
// resolve without VAC duplicating them. stdout is piped straight to the writer —
// never buffered in full — so a large dump doesn't blow the RAM budget. stderr
// is captured separately and surfaced in the error on a non-zero exit.
func (c *Compose) Exec(ctx context.Context, containerID string, cmd []string, out io.Writer) error {
	if containerID == "" {
		return errors.New("dockercli: exec needs a container id")
	}
	if len(cmd) == 0 {
		return errors.New("dockercli: exec needs a command")
	}
	// `sh -c` so the user command can use pipes/redirection/$VARS exactly as it
	// would inside the container. cmd is joined into a single shell string.
	shell := strings.Join(cmd, " ")
	command := c.command(ctx, "", "exec", containerID, "sh", "-c", shell)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrDockerMissing
		}
		return err
	}
	// Copy stdout to the destination as it arrives. A copy error is recorded but
	// we still Wait so the child is reaped and its exit status is observed.
	_, copyErr := io.Copy(out, stdout)
	waitErr := command.Wait()
	if waitErr != nil {
		return mapCmdError(waitErr, stderr.Bytes())
	}
	if copyErr != nil {
		return fmt.Errorf("dockercli: exec stream: %w", copyErr)
	}
	return nil
}

// command builds an *exec.Cmd with a minimal explicit env. We never inherit
// os.Environ — that would leak VAC_MASTER_KEY into the child.
func (c *Compose) command(ctx context.Context, wd string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // G204: deliberate single docker exec entry point; args are built internally, not from user input
	if wd != "" {
		cmd.Dir = wd
	}
	env := []string{
		"PATH=" + getPath(),
		"HOME=" + os.TempDir(),
	}
	if c.Socket != "" {
		env = append(env, "DOCKER_HOST=unix://"+c.Socket)
	}
	env = append(env, c.ExtraEnv...)
	cmd.Env = env
	return cmd
}

func getPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

// runStreaming pipes combined stdout+stderr into `out`, one line at a time.
// Output is line-buffered — partial lines are held until newline arrives.
func runStreaming(cmd *exec.Cmd, out io.Writer) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // interleave — git/docker do this naturally
	_ = stdout              // keep the reference live for the goroutine below
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrDockerMissing
		}
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		_, _ = out.Write(append(scanner.Bytes(), '\n'))
	}
	// A scanner error (most likely bufio.ErrTooLong on a >1 MiB build line) stops
	// the loop early and silently swallows the rest of the stream. Surface it in
	// the log so a truncated build isn't mistaken for a complete one; still drain
	// the process via Wait so it's reaped and its exit status observed.
	if serr := scanner.Err(); serr != nil {
		_, _ = io.WriteString(out, "\n[vac] build log truncated: "+serr.Error()+"\n")
	}
	if err := cmd.Wait(); err != nil {
		return mapCmdError(err, nil)
	}
	return nil
}

func mapCmdError(err error, output []byte) error {
	if err == nil {
		return nil
	}
	if execErr := new(exec.Error); errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound {
		return ErrDockerMissing
	}
	msg := strings.TrimSpace(string(output))
	if msg == "" {
		return fmt.Errorf("dockercli: %w", err)
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("dockercli: %w: %s", err, msg)
}
