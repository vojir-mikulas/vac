// Package gitcli shells out to the system `git` binary. We use the CLI
// instead of go-git because git's SSH host-key handling, submodule support
// and protocol coverage are years ahead of the Go re-implementation.
//
// All callers must pass a context with a timeout — git happily blocks on a
// hung TCP connection forever if asked. BatchMode=yes prevents the SSH
// client from ever prompting for input.
package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Typed errors so the test-connection handler can render targeted messages.
var (
	ErrAuth           = errors.New("gitcli: authentication failed")
	ErrRepoNotFound   = errors.New("gitcli: repository not found")
	ErrBranchNotFound = errors.New("gitcli: branch not found")
	ErrNetwork        = errors.New("gitcli: network unreachable")
	ErrGitMissing     = errors.New("gitcli: git binary not on PATH")
)

// LsRemote is the pre-deploy probe — fast, no working tree on disk. If
// `branch` is non-empty git exits 2 when the ref is missing, which we map
// to ErrBranchNotFound.
func LsRemote(ctx context.Context, gitURL, branch, sshKeyPath string) error {
	args := []string{"ls-remote", "--exit-code", gitURL}
	if branch != "" {
		args = append(args, branch)
	}
	out, err := run(ctx, "", buildEnv(sshKeyPath), args...)
	if err == nil {
		return nil
	}
	return classify(err, out, branch != "")
}

// Clone shallow-clones a single branch. `dest` must not exist yet.
func Clone(ctx context.Context, gitURL, dest, branch, sshKeyPath string) error {
	args := []string{"clone", "--depth=1", "--single-branch"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, gitURL, dest)
	out, err := run(ctx, "", buildEnv(sshKeyPath), args...)
	if err == nil {
		return nil
	}
	return classify(err, out, branch != "")
}

// Pull treats the worktree as disposable: fetch + hard-reset to origin/<branch>.
// No merge, no conflict surface. The caller already accepted that local
// edits in the working tree are not preserved across deploys.
func Pull(ctx context.Context, dest, branch, sshKeyPath string) error {
	env := buildEnv(sshKeyPath)
	if out, err := run(ctx, "", env, "-C", dest, "fetch", "origin", branch); err != nil {
		return classify(err, out, true)
	}
	if out, err := run(ctx, "", env, "-C", dest, "reset", "--hard", "origin/"+branch); err != nil {
		return classify(err, out, true)
	}
	return nil
}

// FetchCommit pins an existing working clone to a specific commit. The deploy
// clone is shallow (--depth=1 --single-branch), so an older SHA — e.g. the one
// a rollback targets — usually isn't present yet, so we fetch it before
// checking it out detached.
//
// Fast path: fetch just the target object — works on hosts that allow
// reachable-SHA1 wants (GitHub/GitLab). Fallback for hosts that refuse a by-SHA
// fetch: deepen the shallow clone to full history. A rollback target is always
// an ancestor of the tracked branch, so deepening makes it available; the
// deepen is a harmless no-op on an already-complete repo.
func FetchCommit(ctx context.Context, dest, sha, sshKeyPath string) error {
	env := buildEnv(sshKeyPath)
	if _, err := run(ctx, "", env, "-C", dest, "fetch", "--depth=1", "origin", sha); err != nil {
		if out, err := run(ctx, "", env, "-C", dest, "fetch", "--depth=2147483647", "origin"); err != nil {
			return classify(err, out, false)
		}
	}
	if out, err := run(ctx, "", env, "-C", dest, "checkout", "--detach", sha); err != nil {
		return classify(err, out, false)
	}
	return nil
}

// HeadCommit returns the short SHA + first line of the commit message of
// HEAD inside `dest`. Used by the pipeline to populate deployments.commit_*.
func HeadCommit(ctx context.Context, dest string) (sha, message string, err error) {
	out, runErr := run(ctx, "", nil, "-C", dest, "log", "-1", "--pretty=format:%H%n%s")
	if runErr != nil {
		return "", "", runErr
	}
	lines := strings.SplitN(strings.TrimRight(string(out), "\n"), "\n", 2)
	if len(lines) == 0 {
		return "", "", nil
	}
	sha = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		message = strings.TrimSpace(lines[1])
	}
	return sha, message, nil
}

// run is the single os/exec entry point so the env handling is consistent.
// Returns combined stdout+stderr — git interleaves its progress output and
// we want both for classification.
func run(ctx context.Context, wd string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if wd != "" {
		cmd.Dir = wd
	}
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return out, err
}

// buildEnv produces the minimal env git needs. We never inherit os.Environ
// because that would leak VAC_MASTER_KEY (and friends) into the child.
func buildEnv(sshKeyPath string) []string {
	env := []string{
		"PATH=" + getPath(),
		"HOME=" + os.TempDir(),
		// Stop git from asking the controlling tty for a username / password.
		"GIT_TERMINAL_PROMPT=0",
	}
	if sshKeyPath != "" {
		// accept-new instead of yes/no/no — first contact records the host
		// key, subsequent contacts verify it. UserKnownHostsFile=/dev/null
		// keeps the recording from leaking between apps. %q shell-quotes
		// the key path — os.CreateTemp avoids spaces in practice, but
		// belt-and-braces against any future change to the temp path.
		env = append(env,
			fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %q -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null -o BatchMode=yes -o LogLevel=ERROR", sshKeyPath),
		)
	}
	return env
}

func getPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

// classify inspects git's stderr and exit code to map raw failures into the
// typed errors used by the test-connection handler. `branchProvided` shifts
// the interpretation of exit-2: with --exit-code that means "ref not found".
func classify(err error, output []byte, branchProvided bool) error {
	if err == nil {
		return nil
	}
	if execErr := new(exec.Error); errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound {
		return ErrGitMissing
	}
	out := strings.ToLower(string(output))

	switch {
	case strings.Contains(out, "permission denied") ||
		strings.Contains(out, "publickey") ||
		strings.Contains(out, "authentication failed"):
		return fmt.Errorf("%w: %s", ErrAuth, firstLine(output))
	case strings.Contains(out, "repository not found") ||
		strings.Contains(out, "does not exist") ||
		strings.Contains(out, "could not read from remote"):
		return fmt.Errorf("%w: %s", ErrRepoNotFound, firstLine(output))
	case strings.Contains(out, "couldn't find remote ref") ||
		strings.Contains(out, "remote branch") && strings.Contains(out, "not found"):
		return fmt.Errorf("%w: %s", ErrBranchNotFound, firstLine(output))
	case strings.Contains(out, "could not resolve host") ||
		strings.Contains(out, "connection refused") ||
		strings.Contains(out, "network is unreachable") ||
		strings.Contains(out, "operation timed out"):
		return fmt.Errorf("%w: %s", ErrNetwork, firstLine(output))
	}

	// --exit-code + branch present: exit 2 = ref missing.
	var exitErr *exec.ExitError
	if branchProvided && errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		return fmt.Errorf("%w: %s", ErrBranchNotFound, firstLine(output))
	}
	return fmt.Errorf("gitcli: %w: %s", err, firstLine(output))
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
