package gitcli_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/gitcli"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary not on PATH: %v", err)
	}
}

// initBareRepo creates a tiny bare repo + a working clone with one commit
// on the configured branch. The bare repo path is returned so tests can use
// it as `gitURL`.
func initBareRepo(t *testing.T, branch string) string {
	t.Helper()
	requireGit(t)

	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	mustRun(t, root, "git", "init", "--bare", "-b", branch, bare)
	mustRun(t, root, "git", "init", "-b", branch, work)
	mustRun(t, work, "git", "config", "user.email", "test@example.com")
	mustRun(t, work, "git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, work, "git", "add", "README.md")
	mustRun(t, work, "git", "commit", "-m", "initial")
	mustRun(t, work, "git", "remote", "add", "origin", bare)
	mustRun(t, work, "git", "push", "origin", branch)
	return bare
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v — %s", name, strings.Join(args, " "), err, out)
	}
}

func TestLsRemote_HappyPath(t *testing.T) {
	requireGit(t)
	bare := initBareRepo(t, "main")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gitcli.LsRemote(ctx, bare, "main", ""); err != nil {
		t.Fatalf("ls-remote against fixture failed: %v", err)
	}
}

func TestLsRemote_BranchMissing(t *testing.T) {
	requireGit(t)
	bare := initBareRepo(t, "main")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := gitcli.LsRemote(ctx, bare, "nope-not-here", "")
	if !errors.Is(err, gitcli.ErrBranchNotFound) {
		t.Errorf("err = %v, want ErrBranchNotFound", err)
	}
}

func TestLsRemote_RepoMissing(t *testing.T) {
	requireGit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := gitcli.LsRemote(ctx, "/no/such/path/to/repo.git", "main", "")
	if !errors.Is(err, gitcli.ErrRepoNotFound) {
		t.Errorf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestCloneAndHeadCommit(t *testing.T) {
	requireGit(t)
	bare := initBareRepo(t, "main")
	dest := filepath.Join(t.TempDir(), "clone")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := gitcli.Clone(ctx, bare, dest, "main", ""); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	sha, msg, err := gitcli.HeadCommit(ctx, dest)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if len(sha) < 7 {
		t.Errorf("sha = %q", sha)
	}
	if msg != "initial" {
		t.Errorf("msg = %q, want 'initial'", msg)
	}
}

// initTwoCommitRepo builds a bare repo with two commits on `branch` and
// returns (bareURL, firstCommitSHA). When allowSHAFetch is true the bare repo
// permits fetching an arbitrary SHA (mirroring the reachable-SHA1 support
// GitHub/GitLab provide); when false, FetchCommit must fall back to deepening.
func initTwoCommitRepo(t *testing.T, branch string, allowSHAFetch bool) (bare, firstSHA string) {
	t.Helper()
	requireGit(t)

	root := t.TempDir()
	bare = filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	mustRun(t, root, "git", "init", "--bare", "-b", branch, bare)
	if allowSHAFetch {
		mustRun(t, bare, "git", "config", "uploadpack.allowAnySHA1InWant", "true")
		mustRun(t, bare, "git", "config", "uploadpack.allowReachableSHA1InWant", "true")
	}
	mustRun(t, root, "git", "init", "-b", branch, work)
	mustRun(t, work, "git", "config", "user.email", "test@example.com")
	mustRun(t, work, "git", "config", "user.name", "test")
	mustRun(t, work, "git", "remote", "add", "origin", bare)

	if err := os.WriteFile(filepath.Join(work, "v.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, work, "git", "add", "v.txt")
	mustRun(t, work, "git", "commit", "-m", "first")
	firstSHA = mustOutput(t, work, "git", "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(work, "v.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, work, "git", "add", "v.txt")
	mustRun(t, work, "git", "commit", "-m", "second")
	mustRun(t, work, "git", "push", "origin", branch)
	return bare, firstSHA
}

func mustOutput(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func TestFetchCommit_PinsToPriorCommit(t *testing.T) {
	// Both the by-SHA fast path (host allows it) and the deepen fallback (host
	// refuses by-SHA fetches) must land the shallow clone on the older commit.
	for _, tc := range []struct {
		name          string
		allowSHAFetch bool
	}{
		{"by-sha fast path", true},
		{"deepen fallback", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireGit(t)
			bare, firstSHA := initTwoCommitRepo(t, "main", tc.allowSHAFetch)
			dest := filepath.Join(t.TempDir(), "clone")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Shallow clone gets only the latest commit — the older one isn't present.
			if err := gitcli.Clone(ctx, bare, dest, "main", ""); err != nil {
				t.Fatalf("Clone: %v", err)
			}
			if sha, _, _ := gitcli.HeadCommit(ctx, dest); sha == firstSHA {
				t.Fatal("clone unexpectedly already at the first commit")
			}

			// FetchCommit must fetch + check out the older commit even though the
			// shallow clone didn't have it.
			if err := gitcli.FetchCommit(ctx, dest, firstSHA, ""); err != nil {
				t.Fatalf("FetchCommit: %v", err)
			}
			sha, msg, err := gitcli.HeadCommit(ctx, dest)
			if err != nil {
				t.Fatalf("HeadCommit: %v", err)
			}
			if sha != firstSHA {
				t.Errorf("after FetchCommit HEAD = %q, want %q", sha, firstSHA)
			}
			if msg != "first" {
				t.Errorf("message = %q, want 'first'", msg)
			}
		})
	}
}

func TestPull(t *testing.T) {
	requireGit(t)
	bare := initBareRepo(t, "main")
	dest := filepath.Join(t.TempDir(), "clone")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := gitcli.Clone(ctx, bare, dest, "main", ""); err != nil {
		t.Fatal(err)
	}
	// Pull on an up-to-date clone should be a no-op.
	if err := gitcli.Pull(ctx, dest, "main", ""); err != nil {
		t.Errorf("Pull no-op: %v", err)
	}
}
