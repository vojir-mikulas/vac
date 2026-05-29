package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/vojir-mikulas/vac/api/internal/compose"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/gitcli"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// GitClient lets pipeline tests swap a fake git for the real CLI.
type GitClient interface {
	LsRemote(ctx context.Context, gitURL, branch, sshKeyPath string) error
	Clone(ctx context.Context, gitURL, dest, branch, sshKeyPath string) error
	Pull(ctx context.Context, dest, branch, sshKeyPath string) error
	HeadCommit(ctx context.Context, dest string) (sha, message string, err error)
}

// DockerClient is the slice of dockercli.Compose the pipeline calls.
type DockerClient interface {
	Build(ctx context.Context, projectDir, composeFile, projectName string, out io.Writer) error
	Up(ctx context.Context, projectDir, composeFile, projectName, envFile string) error
	Ps(ctx context.Context, projectName string) ([]dockercli.PsService, error)
}

// realGit adapts the gitcli package functions to GitClient.
type realGit struct{}

func (realGit) LsRemote(ctx context.Context, u, b, k string) error {
	return gitcli.LsRemote(ctx, u, b, k)
}
func (realGit) Clone(ctx context.Context, u, d, b, k string) error {
	return gitcli.Clone(ctx, u, d, b, k)
}
func (realGit) Pull(ctx context.Context, d, b, k string) error { return gitcli.Pull(ctx, d, b, k) }
func (realGit) HeadCommit(ctx context.Context, d string) (string, string, error) {
	return gitcli.HeadCommit(ctx, d)
}

// Pipeline runs the build steps for one deployment. It is constructed once
// at server startup and reused by the worker for every deployment.
type Pipeline struct {
	Store   *store.Store
	Keys    *sshkey.Manager
	Box     *crypto.Box
	Docker  DockerClient
	Git     GitClient
	WorkDir string
	Logger  *slog.Logger
}

// NewPipeline wires the production dependencies. Callers can patch the
// fields post-construction in tests (e.g. swap Git or Docker for fakes).
func NewPipeline(s *store.Store, keys *sshkey.Manager, box *crypto.Box, docker *dockercli.Compose, workDir string, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		Store:   s,
		Keys:    keys,
		Box:     box,
		Docker:  docker,
		Git:     realGit{},
		WorkDir: workDir,
		Logger:  logger,
	}
}

// Run executes the full pipeline for `deploymentID`. The deployment row
// must already exist (the handler creates it before enqueueing). On any
// step failure the deployment is marked `error` and the prior stack is
// left running — VAC never tears down on its own.
func (p *Pipeline) Run(ctx context.Context, deploymentID string) (runErr error) {
	d, err := p.Store.GetDeployment(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("pipeline: load deployment: %w", err)
	}
	app, err := p.Store.GetApp(ctx, d.AppID)
	if err != nil {
		return fmt.Errorf("pipeline: load app: %w", err)
	}

	logger := p.Logger.With("deployment_id", deploymentID, "app", app.Slug)
	logger.Info("pipeline: starting")

	// Mark started; any non-nil runErr at the end of the function trips
	// the failure-finishing block below.
	if err := p.Store.MarkDeploymentStarted(ctx, deploymentID); err != nil {
		return fmt.Errorf("pipeline: mark started: %w", err)
	}
	_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusBuilding)

	defer func() {
		if runErr != nil {
			msg := runErr.Error()
			_ = LogSystem(ctx, p.Store, deploymentID, "pipeline failed: "+msg)
			_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusError, &msg)
			_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusError)
		}
	}()

	// ---- Clone / pull ----
	if err := p.setStatus(ctx, deploymentID, DeploymentStatusCloning); err != nil {
		return err
	}
	repoDir := filepath.Join(p.WorkDir, app.Slug, "repo")
	keyPath, cleanupKey, err := p.materialiseKey(ctx, app)
	if err != nil {
		return err
	}
	defer cleanupKey()

	if err := p.cloneOrPull(ctx, app, repoDir, keyPath); err != nil {
		return err
	}
	sha, msg, _ := p.Git.HeadCommit(ctx, repoDir)
	if sha != "" {
		_ = p.Store.SetDeploymentCommit(ctx, deploymentID, &sha, &msg)
		_ = LogSystem(ctx, p.Store, deploymentID, fmt.Sprintf("commit: %s — %s", sha[:min(7, len(sha))], msg))
	}

	// ---- Compose detection / wrap ----
	res, err := compose.Detect(repoDir)
	if err != nil {
		return err
	}
	composeFile := res.Path
	if res.Source == compose.SourceGenerated {
		// The wrap lives inside repo/ so its `build: .` resolves to the
		// repo working tree. The next pull's reset --hard does not touch
		// untracked files, so the wrap survives — but we re-write every
		// deploy in case the template changes.
		wrapPath := filepath.Join(repoDir, "compose.yaml")
		written, werr := compose.Wrap(wrapPath)
		if werr != nil {
			return werr
		}
		composeFile = written
		_ = LogSystem(ctx, p.Store, deploymentID, "no compose file in repo — using auto-generated wrapper for Dockerfile")
	}

	// Compose hash gives us a stable identifier for "was anything that
	// would affect this deploy actually different from last time".
	if hash, herr := hashFile(composeFile); herr == nil {
		_ = p.Store.SetDeploymentComposeHash(ctx, deploymentID, hash)
	}

	// .dockerignore warning is purely informational.
	if warn := compose.WarnIfMissingDockerignore(repoDir); warn != "" {
		_ = LogSystem(ctx, p.Store, deploymentID, warn)
	}

	// ---- Env file ----
	envFile := filepath.Join(repoDir, ".env")
	if p.Box != nil {
		if err := RenderEnvFile(ctx, p.Store, p.Box, app.ID, envFile); err != nil {
			return err
		}
	} else {
		// Without encryption keys we still create an empty file so the
		// user's compose file's `env_file: -.env` doesn't fail.
		_ = os.WriteFile(envFile, nil, 0o600)
	}

	// ---- Build ----
	if err := p.setStatus(ctx, deploymentID, DeploymentStatusBuilding); err != nil {
		return err
	}
	projectName := composeProject(app.Slug)
	lw := NewLogWriter(ctx, p.Store, deploymentID, store.DeploymentLogStreamStdout, nil)
	if err := p.Docker.Build(ctx, repoDir, composeFile, projectName, lw); err != nil {
		_ = lw.Flush()
		return fmt.Errorf("build: %w", err)
	}
	_ = lw.Flush()

	// ---- Up ----
	if err := p.setStatus(ctx, deploymentID, DeploymentStatusDeploying); err != nil {
		return err
	}
	_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusDeploying)
	if err := p.Docker.Up(ctx, repoDir, composeFile, projectName, envFile); err != nil {
		return fmt.Errorf("up: %w", err)
	}

	// ---- Service detection ----
	if err := p.setStatus(ctx, deploymentID, DeploymentStatusHealthChecking); err != nil {
		return err
	}
	services, err := p.Docker.Ps(ctx, projectName)
	if err != nil {
		return fmt.Errorf("ps: %w", err)
	}
	if err := p.upsertServices(ctx, app.ID, services); err != nil {
		return err
	}

	// ---- Health check (filled in by M9 — placeholder always passes) ----
	p.healthCheck(ctx, services)

	// ---- Done ----
	_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusRunning, nil)
	_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusRunning)
	_ = LogSystem(ctx, p.Store, deploymentID, "pipeline: complete")
	logger.Info("pipeline: done")
	return nil
}

func (p *Pipeline) setStatus(ctx context.Context, deploymentID, status string) error {
	if err := p.Store.UpdateDeploymentStatus(ctx, deploymentID, status, nil); err != nil {
		return fmt.Errorf("pipeline: set status %s: %w", status, err)
	}
	return nil
}

func (p *Pipeline) materialiseKey(ctx context.Context, app store.App) (string, func(), error) {
	if !isSSHRepoURL(app.GitURL) {
		return "", func() {}, nil
	}
	if _, getErr := p.Keys.Get(ctx, app.ID); errors.Is(getErr, store.ErrNotFound) {
		if _, mintErr := p.Keys.Mint(ctx, app); mintErr != nil {
			return "", func() {}, mintErr
		}
	} else if getErr != nil {
		return "", func() {}, getErr
	}
	pem, err := p.Keys.OpenPrivateKey(ctx, app.ID)
	if err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp("", "vac-ssh-*")
	if err != nil {
		return "", func() {}, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	if _, err := f.Write(pem); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	_ = f.Close()
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

func (p *Pipeline) cloneOrPull(ctx context.Context, app store.App, dest, sshKeyPath string) error {
	if dirExists(dest) {
		return p.Git.Pull(ctx, dest, app.GitBranch, sshKeyPath)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("pipeline: mkdir workdir: %w", err)
	}
	return p.Git.Clone(ctx, app.GitURL, dest, app.GitBranch, sshKeyPath)
}

func (p *Pipeline) upsertServices(ctx context.Context, appID string, services []dockercli.PsService) error {
	seen := make(map[string]bool, len(services))
	for _, s := range services {
		seen[s.Service] = true
		containerID := s.ID
		port := s.FirstPublishedPort()
		var (
			cidPtr  *string
			portPtr *int
		)
		if containerID != "" {
			cidPtr = &containerID
		}
		if port > 0 {
			portPtr = &port
		}
		status := MapPsStateToServiceStatus(s.State)
		if _, err := p.Store.UpsertService(ctx, appID, s.Service, cidPtr, portPtr, status); err != nil {
			return fmt.Errorf("upsert service %s: %w", s.Service, err)
		}
	}
	// Remove services that disappeared from the compose project.
	existing, err := p.Store.ListServicesForApp(ctx, appID)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if !seen[e.ServiceName] {
			_ = p.Store.DeleteService(ctx, appID, e.ServiceName)
		}
	}
	return nil
}

// healthCheck is a stub in M7 — M9 replaces this with real HTTP probes.
// Returning silently is fine: a deploy is considered "running" once
// `docker compose up` returned and `ps` showed the services.
func (p *Pipeline) healthCheck(_ context.Context, _ []dockercli.PsService) {
	// no-op until M9
}

// composeProject is the docker compose project name VAC uses for every
// user stack. Prefixing with `vac-` keeps it distinct from any compose
// projects the host operator runs by hand.
func composeProject(slug string) string { return "vac-" + slug }

// isSSHRepoURL mirrors handler.isSSHRepoURL — both keep their copy so neither
// package has to depend on the other.
func isSSHRepoURL(u string) bool {
	switch {
	case len(u) >= 6 && u[:6] == "ssh://":
		return true
	case len(u) > 4 && u[:4] == "git@":
		for i := 4; i < len(u); i++ {
			if u[i] == ':' {
				return true
			}
		}
	}
	return false
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is VAC-controlled
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
