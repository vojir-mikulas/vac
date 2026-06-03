package dockercli

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// PtySession is an interactive `docker exec -i -t` attached to a pseudo-terminal.
// It is the privileged shell path (P3.4): the control plane shells into a *user
// app* container. Read/Write move raw terminal bytes; Resize forwards the
// client's xterm dimensions; Close kills the exec and reaps it so no orphan
// `docker exec` lingers after the WebSocket drops.
type PtySession struct {
	pty *os.File
	cmd *exec.Cmd
}

// ExecInteractive starts an interactive shell inside the container over a PTY.
// An empty cmd defaults to `sh` (the lowest-common-denominator shell present in
// alpine/distroless-ish images). The caller owns the returned session and must
// Close it. Reads block until the shell produces output; the session ends when
// the shell exits or Close is called.
func (c *Compose) ExecInteractive(ctx context.Context, containerID string, cmd []string) (*PtySession, error) {
	if containerID == "" {
		return nil, errors.New("dockercli: exec needs a container id")
	}
	if len(cmd) == 0 {
		cmd = []string{"sh"}
	}
	// -i keeps stdin open, -t allocates a TTY. The pty we attach below is what
	// makes `docker exec -t` see a terminal even though vac-api itself has none.
	args := append([]string{"exec", "-i", "-t", containerID}, cmd...)
	command := c.command(ctx, "", args...)
	f, err := pty.Start(command)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrDockerMissing
		}
		return nil, err
	}
	return &PtySession{pty: f, cmd: command}, nil
}

// Read pulls the next chunk of terminal output. Returns io.EOF once the shell
// exits and the pty drains.
func (s *PtySession) Read(p []byte) (int, error) { return s.pty.Read(p) }

// Write feeds raw bytes (keystrokes) to the shell's stdin.
func (s *PtySession) Write(p []byte) (int, error) { return s.pty.Write(p) }

// Resize forwards the client terminal's new dimensions so full-screen programs
// (vi, top, …) lay out correctly.
func (s *PtySession) Resize(rows, cols uint16) error {
	return pty.Setsize(s.pty, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close tears the session down: closing the pty signals the shell, and we kill
// then Wait the `docker exec` child so it is reaped rather than orphaned.
func (s *PtySession) Close() error {
	_ = s.pty.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.cmd.Wait()
}
