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
