package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
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
	// FetchCommit pins the working clone to a specific commit (rollback).
	FetchCommit(ctx context.Context, dest, sha, sshKeyPath string) error
	HeadCommit(ctx context.Context, dest string) (sha, message string, err error)
}

// DockerClient is the slice of dockercli.Compose the pipeline calls.
type DockerClient interface {
	Build(ctx context.Context, projectDir, composeFile, projectName string, out io.Writer) error
	Up(ctx context.Context, projectDir, composeFile, projectName, envFile string, overrideFiles ...string) error
	Ps(ctx context.Context, projectName string) ([]dockercli.PsService, error)
	// Config renders the fully merged compose document (include/extends/overrides
	// resolved) so the preflight lints what `up` will actually run.
	Config(ctx context.Context, projectDir, composeFile, projectName string) ([]byte, error)
	// Login authenticates the daemon to a private registry before a pull.
	Login(ctx context.Context, registry, username, password string) error
	// Pull explicitly fetches the project's images (`docker compose pull`) so a
	// moved tag is picked up on redeploy — used for image-sourced apps.
	Pull(ctx context.Context, projectDir, composeFile, projectName string) error
}

// Router projects an app's domains into the reverse proxy and gates a deploy on
// health via Caddy (Phase 3). When nil — tests, or a deploy on a host without
// the proxy wired — the pipeline falls back to its Phase 2 loopback probe.
type Router interface {
	Sync(ctx context.Context, appID string) error
	WaitHealthy(ctx context.Context, appID string) error
}

// TemplateMaterializer copies an add-on template's embedded files into the
// deploy work dir, replacing the git clone for template-sourced apps (Track D /
// D3). nil → template apps fail with a clear error. Implemented by
// addon.Registry. The Track D deploy-pipeline touches stay additive: a clone-step
// branch (Materialize) and a post-up seam that applies a template's declared
// Caddy health-check paths (ServiceHealthPaths) — never a rewrite of
// build/up/health/route.
type TemplateMaterializer interface {
	Materialize(templateID, destDir string) error
	// ServiceHealthPaths returns the template's per-service Caddy health-check
	// paths (service name → path), or nil if it declares none.
	ServiceHealthPaths(templateID string) map[string]string
}

// Reconciler attaches runtime-log followers to an app's freshly-(re)created
// containers after a deploy. Implemented by logstream.Supervisor; nil disables
// the explicit nudge (the supervisor still reconciles off container events).
type Reconciler interface {
	ReconcileApp(ctx context.Context, appID string)
}

// Notifier fires outbound notifications on deploy outcomes. Implemented by
// notify.Dispatcher; nil disables notifications.
type Notifier interface {
	DeploySucceeded(appName, appID, sha, msg string, dur time.Duration)
	DeployFailed(appName, appID, errMsg string, dur time.Duration)
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
func (realGit) FetchCommit(ctx context.Context, d, sha, k string) error {
	return gitcli.FetchCommit(ctx, d, sha, k)
}

func (realGit) HeadCommit(ctx context.Context, d string) (string, string, error) {
	return gitcli.HeadCommit(ctx, d)
}

// Pipeline runs the build steps for one deployment. It is constructed once
// at server startup and reused by the worker for every deployment.
type Pipeline struct {
	Store              *store.Store
	Keys               *sshkey.Manager
	Box                *crypto.Box
	Docker             DockerClient
	Git                GitClient
	HealthChecker      Checker
	Router             Router               // nil → Phase 2 loopback health check fallback
	Hub                Publisher            // nil → no live build-log streaming
	Reconciler         Reconciler           // nil → no explicit log-follower nudge
	Notifier           Notifier             // nil → no deploy notifications
	Templates          TemplateMaterializer // nil → template-sourced apps fail
	WorkDir            string
	HealthCheckTimeout time.Duration
	HealthCheckRetries int
	// AppCPULimit is the box-wide CPU ceiling (in CPUs) applied to every user app
	// container via a `cpus:` compose override. 0 disables it. Sourced from
	// config.AppCPULimit and patched in by main; pairs with App.MemLimitMB.
	AppCPULimit float64
	Logger      *slog.Logger
}

// NewPipeline wires the production dependencies. Callers can patch the
// fields post-construction in tests (e.g. swap Git or Docker for fakes).
func NewPipeline(s *store.Store, keys *sshkey.Manager, box *crypto.Box, docker *dockercli.Compose, workDir string, healthTimeout time.Duration, healthRetries int, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		Store:              s,
		Keys:               keys,
		Box:                box,
		Docker:             docker,
		Git:                realGit{},
		HealthChecker:      HTTPChecker{},
		WorkDir:            workDir,
		HealthCheckTimeout: healthTimeout,
		HealthCheckRetries: healthRetries,
		Logger:             logger,
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
	// A deploy can be cancelled while still queued: the handler settles the row
	// terminal (e.g. `canceled`) before any worker picks it up. Honor that —
	// never run an already-settled deployment.
	if IsTerminalDeploymentStatus(d.Status) {
		p.Logger.Info("pipeline: skipping already-settled deployment",
			"deployment_id", deploymentID, "status", d.Status)
		return nil
	}
	app, err := p.Store.GetApp(ctx, d.AppID)
	if err != nil {
		return fmt.Errorf("pipeline: load app: %w", err)
	}

	logger := p.Logger.With("deployment_id", deploymentID, "app", app.Slug)
	logger.Info("pipeline: starting")
	runStart := time.Now()

	// Tell live build-log subscribers the stream is finished on every exit
	// path (success, error, degraded). Deferred first so it runs last — after
	// the failure-finishing defer below has settled the deployment status.
	defer PublishBuildEnd(p.Hub, deploymentID)

	// Mark started; any non-nil runErr at the end of the function trips
	// the failure-finishing block below.
	if err := p.Store.MarkDeploymentStarted(ctx, deploymentID); err != nil {
		return fmt.Errorf("pipeline: mark started: %w", err)
	}
	_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusBuilding)

	defer func() {
		if runErr != nil {
			msg := runErr.Error()
			_ = p.logSystem(ctx, deploymentID, "pipeline failed: "+msg)
			_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusError, &msg)
			_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusError)
			if p.Notifier != nil {
				p.Notifier.DeployFailed(app.Name, app.ID, msg, time.Since(runStart))
			}
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

	// Template-sourced apps (add-ons) materialize embedded files instead of
	// cloning — the one Track-D branch in the clone step (decision #7 / D3).
	if app.Source == store.AppSourceTemplate {
		if p.Templates == nil {
			return fmt.Errorf("pipeline: app is template-sourced but no template registry is configured")
		}
		if app.TemplateID == nil || *app.TemplateID == "" {
			return fmt.Errorf("pipeline: template app has no template id")
		}
		_ = p.logSystem(ctx, deploymentID, "materializing add-on template: "+*app.TemplateID)
		if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil { //nolint:gosec // G301: app working dir
			return fmt.Errorf("pipeline: mkdir workdir: %w", err)
		}
		if err := p.Templates.Materialize(*app.TemplateID, repoDir); err != nil {
			return fmt.Errorf("pipeline: materialize template: %w", err)
		}
	} else if app.Source == store.AppSourceImage {
		// Image-sourced apps have no repo to clone: the image adapter generates a
		// compose.yaml into an empty work dir, and `up` pulls the ref. Same shape
		// as a template app (no clone, no commit) — the source is a registry ref.
		_ = p.logSystem(ctx, deploymentID, "image source: no clone")
		if err := os.MkdirAll(repoDir, 0o755); err != nil { //nolint:gosec // G301: app working dir
			return fmt.Errorf("pipeline: mkdir workdir: %w", err)
		}
	} else if err := p.cloneOrPull(ctx, app, repoDir, keyPath); err != nil {
		return err
	}
	// Rollback pins the clone to a prior commit. The deployment row already
	// carries the target SHA (copied from the source at enqueue), so we fetch
	// + checkout it here, before reading HEAD — the rest of the pipeline then
	// builds that exact commit through the normal health-gated path.
	if target := rollbackTargetSHA(d); target != "" {
		short := target[:min(7, len(target))]
		_ = p.logSystem(ctx, deploymentID, "rollback: pinning to commit "+short)
		if err := p.Git.FetchCommit(ctx, repoDir, target, keyPath); err != nil {
			return fmt.Errorf("rollback checkout %s: %w", short, err)
		}
	}
	sha, msg, _ := p.Git.HeadCommit(ctx, repoDir)
	if sha != "" {
		_ = p.Store.SetDeploymentCommit(ctx, deploymentID, &sha, &msg)
		_ = p.logSystem(ctx, deploymentID, fmt.Sprintf("commit: %s — %s", sha[:min(7, len(sha))], msg))
	}

	// ---- Build adapter: resolve/produce the compose file ----
	// Adapters formalize every build source (compose / dockerfile / framework /
	// static) down to a compose file VAC builds & ups; the rest of the pipeline
	// stays compose-driven, preserving the vac-edge routing + Caddy health
	// invariants. build_kind="auto" detects the kind from the cloned repo.
	cfg, err := adapter.ParseConfig(app.BuildConfig)
	if err != nil {
		return err
	}
	// Back-compat: an empty configured compose path falls back to the legacy
	// per-app compose_file column.
	if cfg.ComposePath == "" {
		cfg.ComposePath = app.ComposeFile
	}
	ad, err := adapter.For(app.BuildKind, repoDir)
	if err != nil {
		return err
	}
	composeFile, err := ad.Prepare(ctx, repoDir, cfg)
	if err != nil {
		return err
	}
	_ = p.logSystem(ctx, deploymentID, "build source: "+ad.Kind())

	// Compose hash gives us a stable identifier for "was anything that
	// would affect this deploy actually different from last time".
	if hash, herr := hashFile(composeFile); herr == nil {
		_ = p.Store.SetDeploymentComposeHash(ctx, deploymentID, hash)
	}

	// .dockerignore warning is purely informational.
	if warn := compose.WarnIfMissingDockerignore(repoDir); warn != "" {
		_ = p.logSystem(ctx, deploymentID, warn)
	}

	// ---- Compose preflight (plan 16 / Track E) ----
	// Lint the *resolved* compose for VAC-incompatible constructs before the
	// expensive build/up. Hard findings block here with the same transparent-
	// failure shape as a health-check failure (running stack keeps serving);
	// warnings are logged and the deploy proceeds. allow_unsafe_compose
	// downgrades the edge-conflict class (the operator's call) but never the
	// host-escape class.
	// Resolve the merged compose (include/extends/override files + interpolation)
	// via `docker compose config` and lint THAT, so a host-escape construct hidden
	// behind an `include:`/`extends:` — which `up` resolves but a raw-file parse
	// never sees — can't slip past. Fail CLOSED: a parse failure blocks the deploy
	// rather than silently skipping the guard and building the file anyway.
	preflightProject := composeProject(app.Slug)
	var (
		findings []compose.Finding
		perr     error
	)
	if resolved, cerr := p.Docker.Config(ctx, repoDir, composeFile, preflightProject); cerr == nil {
		findings, perr = compose.PreflightBytes(resolved)
	} else {
		// `docker compose config` itself failed (e.g. a required `${VAR:?}` not yet
		// in the env file we render later): fall back to linting the raw file so the
		// guard still runs — and a genuine parse error below still blocks.
		_ = p.logSystem(ctx, deploymentID, "compose preflight: resolved config unavailable, linting raw compose ("+cerr.Error()+")")
		findings, perr = compose.Preflight(composeFile)
	}
	if perr != nil {
		msg := "compose preflight failed: " + perr.Error()
		_ = p.logSystem(ctx, deploymentID, msg)
		_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusError, &msg)
		_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusDegraded)
		markHTTPServicesDegraded(ctx, p.Store, app.ID)
		logger.Warn("pipeline: blocked — compose preflight could not parse the compose")
		if p.Notifier != nil {
			p.Notifier.DeployFailed(app.Name, app.ID, msg, time.Since(runStart))
		}
		return nil
	}
	var blocking []compose.Finding
	for _, f := range findings {
		_ = p.logSystem(ctx, deploymentID, f.Format())
		if f.Severity == compose.SeverityError && (!cfg.AllowUnsafeCompose || f.IsHostEscape()) {
			blocking = append(blocking, f)
		}
	}
	if len(blocking) > 0 {
		msg := "compose preflight failed:\n" + compose.JoinFindings(blocking)
		_ = p.logSystem(ctx, deploymentID, msg)
		_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusError, &msg)
		_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusDegraded)
		markHTTPServicesDegraded(ctx, p.Store, app.ID)
		logger.Warn("pipeline: blocked by compose preflight")
		if p.Notifier != nil {
			p.Notifier.DeployFailed(app.Name, app.ID, msg, time.Since(runStart))
		}
		return nil
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
	lw := NewLogWriter(ctx, p.Store, p.Hub, deploymentID, store.DeploymentLogStreamStdout, nil)
	if err := p.Docker.Build(ctx, repoDir, composeFile, projectName, lw); err != nil {
		_ = lw.Flush()
		return fmt.Errorf("build: %w", err)
	}
	_ = lw.Flush()

	// ---- Pull (image-sourced apps) ----
	// `build` is a no-op for an image app (no `build:` context), so the real
	// fetch is an explicit `docker compose pull` — preceded by a `docker login`
	// when the app carries private-registry creds. This also re-pulls a moved tag
	// (`:latest`) on redeploy, which `up` alone would not. A pull failure is a
	// transparent deploy error (handled like a build failure); the prior stack
	// keeps serving.
	if app.Source == store.AppSourceImage {
		if err := p.registryPull(ctx, deploymentID, app, repoDir, composeFile, projectName); err != nil {
			return err
		}
	}

	// ---- Up ----
	if err := p.setStatus(ctx, deploymentID, DeploymentStatusDeploying); err != nil {
		return err
	}
	_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusDeploying)
	// Per-app resource limits (plan 06 / Track B): write a resource override and
	// merge it over the user's compose so one container can't OOM or pin the box.
	// RAM is per-app (App.MemLimitMB); CPU is the box-wide AppCPULimit knob.
	// Additive — an extra `-f` file, never a rewrite of the user's compose.
	// (Track B touch of the deploy path; coordinate at merge with Deploy Core.)
	var overrides []string
	memLimitMB := 0
	if app.MemLimitMB != nil {
		memLimitMB = *app.MemLimitMB
	}
	if memLimitMB > 0 || p.AppCPULimit > 0 {
		if ovr, oerr := compose.WriteResourceOverride(composeFile, memLimitMB, p.AppCPULimit); oerr != nil {
			_ = p.logSystem(ctx, deploymentID, "warning: could not apply resource limits: "+oerr.Error())
		} else if ovr != "" {
			overrides = append(overrides, ovr)
		}
	}
	if err := p.Docker.Up(ctx, repoDir, composeFile, projectName, envFile, overrides...); err != nil {
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
	if err := p.upsertServices(ctx, app.ID, services, composeFile); err != nil {
		return err
	}

	// Apply any health-check paths the add-on template declares, now that the
	// service rows exist. Without this, Caddy active-health-checks "/" — which
	// for Grafana is a 302 → /login (not 2xx), so the upstream never goes
	// healthy and every request 503s. Only fills services with no operator-set
	// path, so a manual override via PatchAppService still sticks across redeploys.
	if app.Source == store.AppSourceTemplate && app.TemplateID != nil && p.Templates != nil {
		p.applyTemplateHealthPaths(ctx, app.ID, *app.TemplateID)
	}

	// Attach runtime-log followers to the freshly-(re)created containers now
	// that their ids are persisted, so logs stream from the new generation.
	if p.Reconciler != nil {
		p.Reconciler.ReconcileApp(ctx, app.ID)
	}

	// ---- Routing + health ----
	// Phase 3: route through Caddy over vac-edge, then gate on Caddy's active
	// health check (vac-api is off vac-edge, so it can't probe directly). The
	// ordering matters — Caddy must be proxying to the upstream before it can
	// health-check it. Routing pushes are eventual/best-effort; a health
	// failure is a real outcome (app → degraded, deploy → error).
	if p.Router != nil {
		// Auto-subdomains are derived from the app's services + base domain at
		// sync time (plan 09 F1) — no explicit assignment step needed.
		if err := p.Router.Sync(ctx, app.ID); err != nil {
			_ = p.logSystem(ctx, deploymentID, "warning: route sync failed (will reconcile): "+err.Error())
		}
		if err := p.Router.WaitHealthy(ctx, app.ID); err != nil {
			msg := "health check failed: " + err.Error()
			_ = p.logSystem(ctx, deploymentID, msg)
			_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusError, &msg)
			_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusDegraded)
			markHTTPServicesDegraded(ctx, p.Store, app.ID)
			logger.Warn("pipeline: degraded — upstream did not become healthy")
			if p.Notifier != nil {
				p.Notifier.DeployFailed(app.Name, app.ID, msg, time.Since(runStart))
			}
			return nil
		}
	} else {
		if err := p.healthCheck(ctx, deploymentID, services); err != nil {
			return err
		}
	}

	// ---- Done ----
	_ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusRunning, nil)
	_ = p.Store.SetAppStatus(ctx, app.ID, AppStatusRunning)
	_ = p.logSystem(ctx, deploymentID, "pipeline: complete")
	logger.Info("pipeline: done")
	if p.Notifier != nil {
		p.Notifier.DeploySucceeded(app.Name, app.ID, sha, msg, time.Since(runStart))
	}
	return nil
}

// registryPull logs the daemon in to the app's private registry (when creds are
// stored) and then explicitly pulls the project's image. For a public image the
// login is skipped and only the pull runs. Credentials are opened from the
// sealed column with the master key — a set-but-unreadable credential (no key)
// is a clear error rather than a silent fall-through to an unauthenticated pull.
func (p *Pipeline) registryPull(ctx context.Context, deploymentID string, app store.App, projectDir, composeFile, projectName string) error {
	enc, err := p.Store.GetAppRegistryAuth(ctx, app.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("load registry auth: %w", err)
	}
	if len(enc) > 0 {
		if p.Box == nil {
			return errors.New("registry credentials are set but VAC_MASTER_KEY is not configured")
		}
		plain, oerr := p.Box.Open(enc)
		if oerr != nil {
			return fmt.Errorf("decrypt registry auth: %w", oerr)
		}
		var creds store.RegistryAuth
		if jerr := json.Unmarshal(plain, &creds); jerr != nil {
			return fmt.Errorf("parse registry auth: %w", jerr)
		}
		_ = p.logSystem(ctx, deploymentID, "registry login: "+registryLabel(creds.Registry))
		if lerr := p.Docker.Login(ctx, creds.Registry, creds.Username, creds.Password); lerr != nil {
			return fmt.Errorf("registry login: %w", lerr)
		}
	}
	_ = p.logSystem(ctx, deploymentID, "pulling image")
	if err := p.Docker.Pull(ctx, projectDir, composeFile, projectName); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
}

// registryLabel is the human label for a registry host in the deploy log; an
// empty host means the Docker Hub default.
func registryLabel(registry string) string {
	if registry == "" {
		return "docker.io"
	}
	return registry
}

// logSystem persists a pipeline-level system message and tees it to live
// build-log subscribers via the hub (nil-safe).
func (p *Pipeline) logSystem(ctx context.Context, deploymentID, msg string) error {
	return LogSystem(ctx, p.Store, p.Hub, deploymentID, msg)
}

func (p *Pipeline) setStatus(ctx context.Context, deploymentID, status string) error {
	if err := p.Store.UpdateDeploymentStatus(ctx, deploymentID, status, nil); err != nil {
		return fmt.Errorf("pipeline: set status %s: %w", status, err)
	}
	// Nudge the live deploy-queue panel on every step transition so it reflects
	// cloning → building → … without waiting for the deploy to settle.
	PublishDeploymentsChanged(p.Hub)
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
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // G301: app working dir, traversed by the vac-api owner and the docker daemon
		return fmt.Errorf("pipeline: mkdir workdir: %w", err)
	}
	return p.Git.Clone(ctx, app.GitURL, dest, app.GitBranch, sshKeyPath)
}

func (p *Pipeline) upsertServices(ctx context.Context, appID string, services []dockercli.PsService, composeFile string) error {
	// `docker compose ps` only reports host-published mappings, so an
	// `expose`-only service (e.g. the Grafana add-on) yields TargetPort 0 and
	// would never be attached to vac-edge or routed. Fall back to the container
	// port declared in the compose `expose:`/`ports:` target.
	exposed, eerr := compose.ServiceExposedPorts(composeFile)
	if eerr != nil {
		p.Logger.Warn("pipeline: parse compose for exposed ports", "err", eerr)
	}
	// Which services declare a persistent volume — drives the backup nudge.
	withVolumes, verr := compose.ServicesWithVolumes(composeFile)
	if verr != nil {
		p.Logger.Warn("pipeline: parse compose for volumes", "err", verr)
	}
	seen := make(map[string]bool, len(services))
	for _, s := range services {
		seen[s.Service] = true
		containerID := s.ID
		port := s.FirstPublishedPort()
		internal := s.FirstTargetPort()
		if internal == 0 {
			internal = exposed[s.Service]
		}
		var (
			cidPtr      *string
			portPtr     *int
			internalPtr *int
		)
		if containerID != "" {
			cidPtr = &containerID
		}
		if port > 0 {
			portPtr = &port
		}
		if internal > 0 {
			internalPtr = &internal
		}
		status := MapPsStateToServiceStatus(s.State)
		if _, err := p.Store.UpsertService(ctx, appID, s.Service, cidPtr, portPtr, internalPtr, status, withVolumes[s.Service]); err != nil {
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

// applyTemplateHealthPaths sets the Caddy active-health-check path for an add-on
// template's services from its manifest, but only where the operator hasn't
// already set one (HealthPath nil) — so a manual override survives redeploys.
// Best-effort: a failure here is logged, not fatal (the deploy still proceeds).
func (p *Pipeline) applyTemplateHealthPaths(ctx context.Context, appID, templateID string) {
	paths := p.Templates.ServiceHealthPaths(templateID)
	if len(paths) == 0 {
		return
	}
	rows, err := p.Store.ListServicesForApp(ctx, appID)
	if err != nil {
		p.Logger.Warn("pipeline: list services for template health paths", "err", err)
		return
	}
	for _, r := range rows {
		path, ok := paths[r.ServiceName]
		if !ok || path == "" || r.HealthPath != nil {
			continue
		}
		hp := path
		if _, err := p.Store.SetServiceConfig(ctx, appID, r.ServiceName, nil, nil, &hp); err != nil {
			p.Logger.Warn("pipeline: set template health path", "service", r.ServiceName, "err", err)
		}
	}
}

// healthCheck probes each service with a published port. Services with no
// port published are passed through automatically (they may be workers,
// queues, databases — not HTTP services).
func (p *Pipeline) healthCheck(ctx context.Context, deploymentID string, services []dockercli.PsService) error {
	for _, svc := range services {
		port := svc.FirstPublishedPort()
		if port == 0 {
			continue
		}
		url := healthURLForPort(port)
		_ = p.logSystem(ctx, deploymentID, fmt.Sprintf("health check: %s → %s", svc.Service, url))
		if err := CheckWithRetry(ctx, p.HealthChecker, url, p.HealthCheckRetries, p.HealthCheckTimeout); err != nil {
			return fmt.Errorf("health check %s: %w", svc.Service, err)
		}
	}
	return nil
}

// markHTTPServicesDegraded flips the app's HTTP-exposing services (those with
// an internal port, hence a route) to degraded after a failed health gate. The
// stack is up but not serving — workers/DBs without a port are left untouched.
func markHTTPServicesDegraded(ctx context.Context, s *store.Store, appID string) {
	rows, err := s.ListServicesForApp(ctx, appID)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.InternalPort != nil {
			_ = s.UpdateServiceStatus(ctx, appID, r.ServiceName, ServiceStatusDegraded, nil)
		}
	}
}

// rollbackTargetSHA returns the commit a deployment must be pinned to, or ""
// for a normal deploy that should build HEAD. Only a rollback with a known
// source commit pins; a rollback whose source had no recorded SHA falls back
// to a plain HEAD deploy (better than failing the rollback outright).
func rollbackTargetSHA(d store.Deployment) string {
	if d.TriggeredBy == store.TriggeredRollback && d.CommitSHA != nil {
		return *d.CommitSHA
	}
	return ""
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
