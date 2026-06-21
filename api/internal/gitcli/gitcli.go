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
	"path/filepath"
	"strings"
)

// envExampleCandidates are the conventional "template" env filenames we probe
// for at a repo's root, in priority order. The first one present wins.
var envExampleCandidates = []string{
	".env.example",
	".env.sample",
	".env.template",
	".env.dist",
	"example.env",
}

// maxEnvExampleBytes caps how much of a candidate file we read back — these
// files are tiny by nature, so a larger one is almost certainly not an env
// template, and we never want to stream an arbitrary repo file to the client.
const maxEnvExampleBytes = 256 * 1024

// composeCandidates are the conventional compose filenames we probe for at a
// repo's root, in the same priority order package compose uses for deploy-time
// auto-detection. Kept in sync by hand: gitcli stays free of a dependency on
// the higher-level compose package (same trade-off as envExampleCandidates).
var composeCandidates = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yml",
	"docker-compose.yaml",
}

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
// to ErrBranchNotFound. The `--` separator stops git from interpreting a
// hostile URL/ref as an option flag.
func LsRemote(ctx context.Context, gitURL, branch, sshKeyPath string) error {
	args := []string{"ls-remote", "--exit-code", "--", gitURL}
	if branch != "" {
		args = append(args, branch)
	}
	out, err := run(ctx, buildEnv(sshKeyPath), args...)
	if err == nil {
		return nil
	}
	return classify(err, out, branch != "")
}

// Clone shallow-clones a single branch. `dest` must not exist yet. The `--`
// separator before the positional URL/dest keeps a URL that begins with `-`
// from being parsed as a flag (defense-in-depth alongside GIT_ALLOW_PROTOCOL).
func Clone(ctx context.Context, gitURL, dest, branch, sshKeyPath string) error {
	args := []string{"clone", "--depth=1", "--single-branch"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", gitURL, dest)
	out, err := run(ctx, buildEnv(sshKeyPath), args...)
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
	// `--` stops git from parsing a `-`-leading branch as an option flag
	// (defense-in-depth; callers also validate against gitRefRe).
	if out, err := run(ctx, env, "-C", dest, "fetch", "origin", "--", branch); err != nil {
		return classify(err, out, true)
	}
	if out, err := run(ctx, env, "-C", dest, "reset", "--hard", "origin/"+branch); err != nil {
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
	if _, err := run(ctx, env, "-C", dest, "fetch", "--depth=1", "origin", "--", sha); err != nil {
		if out, err := run(ctx, env, "-C", dest, "fetch", "--depth=2147483647", "origin"); err != nil {
			return classify(err, out, false)
		}
	}
	// NB: no `--` separator here — for `checkout` that switches to pathspec
	// interpretation and the SHA would be read as a filename. Flag injection is
	// instead neutralized by requiring sha to be a bare hex object name (it
	// originates from our own HeadCommit, but validate defensively).
	if !isHexObjectName(sha) {
		return fmt.Errorf("gitcli: refusing to check out non-hex ref %q", sha)
	}
	if out, err := run(ctx, env, "-C", dest, "checkout", "--detach", sha); err != nil {
		return classify(err, out, false)
	}
	return nil
}

// isHexObjectName reports whether s is a non-empty string of hex digits — the
// shape of a git object name (full or abbreviated SHA). Used to reject anything
// that could be read as a flag before it reaches `git checkout`.
func isHexObjectName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// HeadCommit returns the short SHA + first line of the commit message of
// HEAD inside `dest`. Used by the pipeline to populate deployments.commit_*.
func HeadCommit(ctx context.Context, dest string) (sha, message string, err error) {
	out, runErr := run(ctx, nil, "-C", dest, "log", "-1", "--pretty=format:%H%n%s")
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

// ReadEnvExample shallow-clones gitURL into a throwaway temp dir and returns the
// first env-example file found at the repo root (its name + contents). A repo
// with no such file yields ("", nil, nil) — a normal "nothing to import", not an
// error. Clone failures (auth, missing repo/branch, network) come back as the
// typed errors so callers can render targeted guidance. Pass sshKeyPath="" for
// public repos; the new-app wizard uses this before a deploy key exists, so
// private SSH repos surface ErrAuth and the UI falls back to manual entry.
func ReadEnvExample(ctx context.Context, gitURL, branch, sshKeyPath string) (string, []byte, error) {
	tmp, err := os.MkdirTemp("", "vac-envex-*")
	if err != nil {
		return "", nil, fmt.Errorf("gitcli: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// Clone requires a non-existent dest, so clone into a child of the temp dir.
	repo := filepath.Join(tmp, "repo")
	if err := Clone(ctx, gitURL, repo, branch, sshKeyPath); err != nil {
		return "", nil, err
	}
	for _, name := range envExampleCandidates {
		b, err := os.ReadFile(filepath.Join(repo, name)) //nolint:gosec // name is from a fixed candidate list; repo is a freshly created temp clone dir
		if err != nil {
			continue
		}
		if len(b) > maxEnvExampleBytes {
			b = b[:maxEnvExampleBytes]
		}
		return name, b, nil
	}
	return "", nil, nil
}

// DetectCompose shallow-clones gitURL into a throwaway temp dir and returns the
// first conventional compose filename present at the repo root (e.g.
// "docker-compose.yml"), or "" when the repo has none — a normal "nothing to
// pre-fill", not an error. Clone failures (auth, missing repo/branch, network)
// come back as the typed errors so callers can render targeted guidance. Like
// ReadEnvExample this runs in the new-app wizard before a deploy key exists, so
// pass sshKeyPath="" for public repos; private SSH repos surface ErrAuth and the
// UI falls back to the manual path input.
func DetectCompose(ctx context.Context, gitURL, branch, sshKeyPath string) (string, error) {
	tmp, err := os.MkdirTemp("", "vac-compose-*")
	if err != nil {
		return "", fmt.Errorf("gitcli: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// Clone requires a non-existent dest, so clone into a child of the temp dir.
	repo := filepath.Join(tmp, "repo")
	if err := Clone(ctx, gitURL, repo, branch, sshKeyPath); err != nil {
		return "", err
	}
	for _, name := range composeCandidates {
		if info, statErr := os.Stat(filepath.Join(repo, name)); statErr == nil && !info.IsDir() {
			return name, nil
		}
	}
	return "", nil
}

// CloneTemp shallow-clones gitURL into a throwaway temp dir and returns the
// checkout path plus a cleanup func the caller MUST invoke (even on error it's
// nil only when err != nil). Used by wizard probes that need to inspect more of
// a repo than a single filename — they run before a deploy key exists, so pass
// sshKeyPath="" (public HTTPS resolves; private SSH comes back as ErrAuth).
func CloneTemp(ctx context.Context, gitURL, branch, sshKeyPath string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "vac-probe-*")
	if err != nil {
		return "", nil, fmt.Errorf("gitcli: temp dir: %w", err)
	}
	repo := filepath.Join(tmp, "repo")
	if err := Clone(ctx, gitURL, repo, branch, sshKeyPath); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	return repo, func() { _ = os.RemoveAll(tmp) }, nil
}

// run is the single os/exec entry point so the env handling is consistent.
// Returns combined stdout+stderr — git interleaves its progress output and
// we want both for classification.
//
// Every invocation hard-disables git's `ext::` transport, which runs an
// arbitrary shell command (e.g. `ext::sh -c id` ⇒ RCE). Upstream callers already
// constrain URLs to http(s)/ssh via gitURLRe, so this is defense-in-depth for
// any path that reaches gitcli with a less-validated URL. The dumb `file`
// transport is left enabled (local fixture clones rely on it); a file:// URL is
// rejected by gitURLRe at the boundary and at worst clones a local repo dir.
func run(ctx context.Context, env []string, args ...string) ([]byte, error) {
	full := append([]string{"-c", "protocol.ext.allow=never"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // G204: deliberate single git exec entry point; args are built internally, not from user input
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
