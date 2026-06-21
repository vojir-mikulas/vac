package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/addon"
	"github.com/vojir-mikulas/vac/api/internal/admin"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/backup"
	"github.com/vojir-mikulas/vac/api/internal/caddy"
	"github.com/vojir-mikulas/vac/api/internal/certcheck"
	"github.com/vojir-mikulas/vac/api/internal/certprobe"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/crashloop"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/diskusage"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/dockerevents"
	"github.com/vojir-mikulas/vac/api/internal/domainstatus"
	"github.com/vojir-mikulas/vac/api/internal/jobs"
	"github.com/vojir-mikulas/vac/api/internal/logstream"
	"github.com/vojir-mikulas/vac/api/internal/notify"
	"github.com/vojir-mikulas/vac/api/internal/preview"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/reqmetrics"
	"github.com/vojir-mikulas/vac/api/internal/retention"
	"github.com/vojir-mikulas/vac/api/internal/scaletozero"
	"github.com/vojir-mikulas/vac/api/internal/security"
	"github.com/vojir-mikulas/vac/api/internal/server"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/stats"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// Build-time metadata injected via -ldflags "-X main.version=..." (see Makefile
// and api/Dockerfile). Defaults make a `go run .` invocation self-describing
// without requiring the caller to set them.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset-password":
			if err := admin.ResetPassword(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, "reset-password:", err)
				os.Exit(1)
			}
			return
		case "export":
			if err := admin.Export(os.Args[2:], os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, "export:", err)
				os.Exit(1)
			}
			return
		case "apply":
			if err := admin.Apply(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, "apply:", err)
				os.Exit(1)
			}
			return
		case "version", "--version", "-v":
			fmt.Printf("vac-api %s\n", version)
			fmt.Printf("  commit: %s\n", commit)
			fmt.Printf("  built:  %s\n", buildDate)
			fmt.Printf("  go:     %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	// Surface build metadata through config so the instance-info endpoint can
	// report it without importing main.
	cfg.Version, cfg.Commit, cfg.BuildDate = version, commit, buildDate
	hostIP := cfg.PublicIPAddr()

	// GOMEMLIMIT / GOGC are applied natively by the Go runtime from the
	// environment (set in compose.prod.yaml to a ~180 MiB soft target so a
	// memory regression paces GC hard and, with the compose hard memory limit,
	// OOMs in testing rather than on a user's box). Log the active soft limit so
	// the RAM benchmark (plan 07) and operators can see what's in effect;
	// SetMemoryLimit(-1) reads the current value without changing it.
	slog.Info("runtime memory limits",
		"gomemlimit_bytes", debug.SetMemoryLimit(-1),
		"gogc", os.Getenv("GOGC"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		slog.Error("database migrate failed", "err", err)
		os.Exit(1)
	}
	slog.Info("database migrations applied")

	st := store.New(pool)

	if n, err := st.CountUsers(ctx); err != nil {
		slog.Warn("could not count users for first-boot detection", "err", err)
	} else if n == 0 {
		token, err := auth.EnsureSetupToken(cfg.WorkDir)
		if err != nil {
			slog.Error("could not create setup token; first-boot will refuse to bootstrap admin", "err", err)
		}
		printFirstBootBanner(cfg, token)
	}

	probeDockerCLI(ctx)

	var box *crypto.Box
	if len(cfg.MasterKey) > 0 {
		if b, err := crypto.New(cfg.MasterKey); err == nil {
			box = b
		} else {
			slog.Warn("crypto box init failed; encryption disabled", "err", err)
		}
	}
	keys := sshkey.NewManager(st, box)
	docker := dockercli.New(cfg.DockerSocket)

	// Phase 4: real-time transport. One hub shared by every producer (deploy
	// pipeline build logs, runtime-log followers, stats collectors) and the WS
	// handlers.
	hub := ws.NewHub()

	// Phase 3: reverse proxy. The Caddy admin client + proxy manager drive
	// routing over the vac-edge network. The manager is the deploy pipeline's
	// Router (auto-domains + route sync + Caddy-gated health).
	caddyClient := caddy.New(cfg.CaddyAdminURL)
	proxyMgr := proxy.New(st, caddyClient, docker, proxy.Config{
		EdgeNetwork:    cfg.EdgeNetwork,
		BaseDomain:     cfg.BaseDomain,
		ControlDomain:  cfg.ControlDomain,
		ControlPort:    cfg.Server.Port,
		HealthInterval: 5 * time.Second,
		HealthTimeout:  cfg.HealthCheckTimeout,
		HealthRetries:  cfg.HealthCheckRetries,
		WakeToken:      cfg.CaddyAskToken,
	}, slog.Default())

	// Apply any runtime base-domain override saved in instance_settings so it
	// survives restarts (the UI writes both the DB and the live manager).
	if settings, err := st.GetInstanceSettings(ctx); err != nil {
		slog.Warn("could not load instance settings; using config base domain", "err", err)
	} else if settings.BaseDomain != "" {
		proxyMgr.SetBaseDomain(settings.BaseDomain)
	}

	// Bring-your-own TLS certs (dns-automation plan B): the manager loads
	// operator-uploaded certs into Caddy over the admin API. It needs the store
	// (to list them) and the crypto box (to open each sealed key). Wire before the
	// base config so DesiredCerts can seed it for restart self-heal. Only wire
	// when the box exists — a typed-nil *crypto.Box in the KeyOpener interface
	// would be non-nil and panic on Open; leaving it unset disables BYO certs.
	if box != nil {
		proxyMgr.SetCertSource(st, box)
	}

	loadCaddyBaseConfig(ctx, cfg, caddyClient, proxyMgr)

	// Outbound notifications (Discord/Slack/email). Stored webhook URLs and the
	// SMTP password are decrypted with the master key; VAC_NOTIFY_* env vars
	// override them.
	var notifyBaseURL string
	if cfg.BaseDomain != "" {
		notifyBaseURL = "https://" + cfg.BaseDomain
	}
	smtpEnv := notify.SMTPEnv{
		Host:     cfg.NotifySMTPHost,
		Port:     cfg.NotifySMTPPort,
		Username: cfg.NotifySMTPUsername,
		Password: cfg.NotifySMTPPassword,
		From:     cfg.NotifySMTPFrom,
		To:       cfg.NotifySMTPTo,
		TLSMode:  cfg.NotifySMTPTLSMode,
	}
	notifier := notify.New(st, box, cfg.NotifyDiscordURL, cfg.NotifySlackURL, notifyBaseURL, smtpEnv, cfg.NotifySMTPAllowPrivate, slog.Default())

	pipeline := deploy.NewPipeline(st, keys, box, docker, cfg.WorkDir, cfg.HealthCheckTimeout, cfg.HealthCheckRetries, slog.Default())
	pipeline.Router = proxyMgr
	pipeline.Hub = hub
	pipeline.Notifier = notifier
	// Auto-maintenance during deploy (maintenance-mode-and-deploy-gates.md, Phase 2):
	// proxy.Manager satisfies deploy.Maintainer.
	pipeline.Maintainer = proxyMgr
	pipeline.AppCPULimit = cfg.AppCPULimit
	// Deploy-pool size is an instance setting (plan 20), applied at boot. Default
	// 1 (strictly serial); the worker clamps to 1..deploy.MaxConcurrency.
	deployConcurrency := 1
	if settings, err := st.GetInstanceSettings(ctx); err == nil && settings.MaxConcurrentDeploys > 0 {
		deployConcurrency = settings.MaxConcurrentDeploys
	}
	worker := deploy.NewPipelineWorker(pipeline, 0, deployConcurrency)
	worker.Start(ctx)

	// Deploy-window sweeper (maintenance-mode-and-deploy-gates.md, Phase 3):
	// releases deploys parked outside their app's window when a window opens. One
	// cheap goroutine — a no-op tick when nothing is parked.
	superviseDaemon(ctx, "deploy-window-sweeper", deploy.NewWindowSweeper(st, worker, slog.Default()).Run)

	// Add-on catalog (Track D / D3). The embedded registry doubles as the deploy
	// pipeline's template materializer (the clone-step seam), so it's wired even
	// when managed services are off — a template app deploys the same way. A
	// parse error here is a build bug; log and continue (no installs exist).
	addonRegistry, addonErr := addon.NewRegistry()
	if addonErr != nil {
		slog.Error("addon registry failed to load; add-ons disabled", "err", addonErr)
		addonRegistry = nil
	} else {
		pipeline.Templates = addonRegistry
	}

	// Security dashboard (plan 15 / E2). The traffic-anomaly monitor rides the
	// existing access-log tail via the collector's observer hook (no second
	// tail). Always-on but cheap (bounded in-memory counters); gated by
	// VAC_SECURITY_MONITOR (default on). Posture and host (fail2ban/firewall)
	// readers are computed on each GET, so they start no goroutine.
	var secTraffic handler.SecurityTraffic
	var secMonitor *security.Monitor
	if cfg.SecurityMonitor {
		secMonitor = security.NewMonitor(security.Config{
			Window:       cfg.SecurityWindow,
			RPSThreshold: cfg.SecurityRPSThreshold,
			ErrThreshold: cfg.SecurityErrThreshold,
			Cooldown:     cfg.SecurityCooldown,
			Allowlist:    cfg.SecurityAllowlist,
		}, notifier, slog.Default())
		secTraffic = secMonitor
	}
	// fail2ban/firewall reader. In the sandboxed container it reads the host
	// agent's snapshot (scripts/vac-security-agent.sh → SecurityDir); when VAC
	// runs directly on the host it falls back to read-only exec.
	secHost := security.NewHost(filepath.Join(cfg.SecurityDir, "host.snapshot"))
	secPosture := security.NewPosture(st, secHost, security.PostureConfig{
		Exposure:         cfg.Exposure,
		MasterKeyPresent: len(cfg.MasterKey) > 0,
		MetricsTokenSet:  cfg.MetricsToken != "",
		BaseDomainSet:    cfg.BaseDomain != "",
		AccessLogEnabled: cfg.CaddyAccessLog != "",
		HostAgentEnabled: cfg.SecurityAgent,
		ExpectFirewall:   cfg.SecurityExpectFirewall,
		ExpectFail2ban:   cfg.SecurityExpectFail2ban,
	})

	// Request-rate metrics: tail Caddy's JSON access log and aggregate per
	// service into the rolling window. When the security monitor is on, it
	// observes each parsed line through the same tail.
	collector := reqmetrics.New(st, cfg.CaddyAccessLog, cfg.CaddyMetricsInterval, slog.Default())
	// Auto-subdomains ({slug}.{base}) route through Caddy but have no domains
	// row, so feed the collector the same derived host set the status engine and
	// CaddyAsk use — otherwise their requests are dropped as unknown hosts.
	collector.SetAutoHostSource(func(ctx context.Context) ([]reqmetrics.AutoHost, error) {
		hosts, err := proxyMgr.AutoHosts(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]reqmetrics.AutoHost, len(hosts))
		for i, h := range hosts {
			out[i] = reqmetrics.AutoHost{Hostname: h.Hostname, AppID: h.AppID, ServiceName: h.ServiceName}
		}
		return out, nil
	})
	if secMonitor != nil {
		collector.SetObserver(secMonitor.Observe)
		superviseDaemon(ctx, "security-monitor", secMonitor.Run)
	}
	superviseDaemon(ctx, "reqmetrics-collector", collector.Run)

	// Real-time stats: per-service collectors (subscriber-gated via the hub's
	// subscribe hooks) plus host vitals. The host request-rate field reuses the
	// Caddy /metrics scrape.
	scraper := reqmetrics.NewScraper(strings.TrimRight(cfg.CaddyAdminURL, "/")+"/metrics", nil)
	hostCollector := stats.NewHostCollector(scraper, cfg.WorkDir, hostIP)
	statsMgr := stats.NewManager(docker, st, hub, hostCollector, cfg.StatsPollInterval, slog.Default())
	hub.SetCallbacks(statsMgr.OnSubscribe, statsMgr.OnUnsubscribe)
	statsMgr.Start(ctx)

	// One docker-events stream fans out to the crash-loop monitor and the
	// runtime-log supervisor (rather than each opening its own).
	eventBus := dockerevents.NewBus(docker, slog.Default())
	superviseDaemon(ctx, "docker-event-bus", eventBus.Run)

	monitor := crashloop.New(eventBus, docker, st, crashloop.Config{
		Threshold: cfg.CrashLoopThreshold,
		Window:    cfg.CrashLoopWindow,
	}, slog.Default())
	monitor.SetNotifier(notifier)
	monitor.SetInspector(docker)
	superviseDaemon(ctx, "crashloop-monitor", monitor.Run)

	// Runtime-log capture: one follower per running container, writing to the
	// DB ring buffer and teeing to the hub. Reconciles on deploy/lifecycle, on
	// container events, and on a periodic resync.
	logSup := logstream.New(docker, st, st, hub, eventBus, logstream.Config{
		RingBuffer: cfg.LogRingBuffer,
	}, slog.Default())
	superviseDaemon(ctx, "logstream-supervisor", logSup.Run)
	pipeline.Reconciler = logSup

	pruner := retention.New(st, docker, retention.Config{
		RuntimeDays:         cfg.LogRetentionDays,
		RequestMetrics:      cfg.RequestMetricsRetention,
		ActivityDays:        cfg.ActivityRetentionDays,
		RingBuffer:          cfg.LogRingBuffer,
		ImageKeepCount:      cfg.ImageKeepCount,
		DeploymentKeepCount: cfg.DeploymentKeepCount,
		BuildCacheMaxBytes:  buildCacheMaxBytes(cfg),
		HourOfDay:           3,
	}, slog.Default())
	superviseDaemon(ctx, "retention-pruner", pruner.Run)

	// Cert-expiry notification (plan 03). Reads each managed host's real TLS
	// expiry by handshaking the proxy with the host's SNI, and alerts once when a
	// cert is within the window and hasn't auto-renewed.
	certChecker := certcheck.New(st, notifier, certProbeAddr(cfg), 10*time.Second, certcheck.Config{
		Threshold: time.Duration(cfg.CertExpiryDays) * 24 * time.Hour,
	}, slog.Default())
	superviseDaemon(ctx, "cert-checker", certChecker.Run)

	// Volume usage & storage alerts. A slow timer samples each app's volume sizes
	// (named volumes via `docker system df -v`, bind mounts via an opt-in bounded
	// walk) and alerts when an app's soft disk budget or the host disk crosses
	// VAC_DISK_ALERT_PERCENT. Mirrors certcheck's long-lived-goroutine shape — it's
	// periodic + persisted, not the real-time WS stats stream.
	diskCollector := diskusage.New(st, docker, notifier, func(ctx context.Context) (uint64, uint64) {
		snap := hostCollector.Snapshot(ctx)
		return snap.DiskUsedBytes, snap.DiskTotalBytes
	}, func(ctx context.Context) uint64 {
		return hostCollector.Snapshot(ctx).MemTotalBytes
	}, diskusage.Config{
		Interval:     cfg.DiskPollInterval,
		AlertPercent: cfg.DiskAlertPercent,
		ScanBinds:    cfg.DiskScanBinds,
	}, slog.Default())
	superviseDaemon(ctx, "disk-collector", diskCollector.Run)

	// Preview deployments (docs/plans/preview-deployments.md). The lifecycle
	// service reuses the deploy worker (enqueue + interrupt), the proxy manager
	// (route teardown + reconcile), and the compose controller (down -v) — only
	// the create-on-branch / reap-on-TTL lifecycle is new. The expirer is a
	// long-lived goroutine mirroring certcheck/diskusage; it no-ops when
	// VAC_PREVIEW_TTL is 0.
	previewSvc := preview.New(st, worker, worker, docker, proxyMgr, notifier, preview.Config{
		MaxPreviews: cfg.MaxPreviews,
		TTL:         cfg.PreviewTTL,
	}, slog.Default())
	superviseDaemon(ctx, "preview-expirer", previewSvc.RunExpirer)

	// Track D (managed services). The backup engine is always constructed so the
	// manual "Back up now" endpoint works the moment the flag is on; the
	// scheduler goroutine, however, only starts when VAC_MANAGED_SERVICES is on
	// AND at least one backup config exists — zero idle footprint otherwise
	// (decision #8). New configs added later are picked up on the next restart.
	backupEngine := backup.NewEngine(st, docker, box, cfg.WorkDir, notifier, slog.Default())
	var dbProvisioner *dbprovision.Provisioner
	var backupRestorer *backup.Restorer
	var backupVerifier *backup.Verifier
	if cfg.ManagedServices {
		if n, err := st.CountBackupConfigs(ctx); err != nil {
			slog.Warn("backup: could not count configs; scheduler not started", "err", err)
		} else if n > 0 {
			superviseDaemon(ctx, "backup-scheduler", backup.NewScheduler(st, backupEngine, slog.Default()).Run)
			slog.Info("backup scheduler started", "configs", n)
		}

		// Managed databases (D2). The provisioner is only built when the gate is
		// open — its engines hold docker/pool handles but start no goroutines.
		dbProvisioner = dbprovision.New(st, box, pool, docker, dbprovision.Config{
			WorkDir:           cfg.WorkDir,
			EdgeNetwork:       cfg.EdgeNetwork,
			MasterKey:         cfg.MasterKey,
			PostgresControlDB: controlDBName(cfg.DatabaseURL),
			ManagedDBIsolated: cfg.ManagedDBIsolated,
		}, slog.Default())
		if cfg.ManagedDBIsolated {
			slog.Info("managed Postgres: isolated mode — provisioning into a dedicated vac-db-managed daemon")
		}

		// Backup restore (plan: docs/plans/backup-restore.md): the inverse of the
		// dump engine. The provisioner doubles as the restore-command resolver
		// (it owns the engine recipes), so it must be built first.
		backupRestorer = backup.NewRestorer(st, docker, box, cfg.WorkDir, dbProvisioner, notifier, slog.Default())

		// Backup verification (restorability check): replays the latest dump into a
		// throwaway scratch DB. The provisioner owns the per-engine verify commands.
		// The sweeper re-verifies stale configs weekly; like the backup scheduler it
		// only starts when ≥1 config exists, so idle footprint stays zero.
		backupVerifier = backup.NewVerifier(st, docker, box, cfg.WorkDir, dbProvisioner, notifier, slog.Default())
		if n, err := st.CountBackupConfigs(ctx); err == nil && n > 0 {
			superviseDaemon(ctx, "backup-verify-scheduler", backup.NewVerifyScheduler(st, backupVerifier, slog.Default()).Run)
		}
	}

	// Scheduled jobs (plan: scheduled-jobs.md). A core feature, not gated by
	// VAC_MANAGED_SERVICES — the engine is always built so "run now" works, and
	// the scheduler goroutine only starts when ≥1 enabled job exists, so idle
	// footprint stays zero. New jobs added later are picked up within the
	// scheduler's idle re-check (or immediately on restart / "run now").
	jobsEngine := jobs.NewEngine(st, docker, notifier, slog.Default())
	if n, err := st.CountScheduledJobs(ctx); err != nil {
		slog.Warn("jobs: could not count jobs; scheduler not started", "err", err)
	} else if n > 0 {
		superviseDaemon(ctx, "jobs-scheduler", jobs.NewScheduler(st, jobsEngine, slog.Default()).Run)
		slog.Info("job scheduler started", "jobs", n)
	}

	// Scale-to-zero (docs/plans/scale-to-zero.md). The waker is always wired so a
	// suspended app can still wake (e.g. if the operator turned the feature off
	// while an app was suspended, its installed wake routes still resolve here).
	// The sweeper goroutine — which creates suspensions — starts only when the
	// master gate is on AND ≥1 app has opted in, mirroring the jobs scheduler so
	// idle footprint stays zero when the feature is unused.
	waker := scaletozero.NewWaker(st, docker, proxyMgr, slog.Default())
	if cfg.IdleSuspend {
		if n, err := st.CountIdleSuspendApps(ctx); err != nil {
			slog.Warn("scaletozero: could not count opted-in apps; sweeper not started", "err", err)
		} else if n > 0 {
			superviseDaemon(ctx, "scale-to-zero-sweeper", scaletozero.NewSweeper(st, waker, cfg.IdleSweepInterval, cfg.IdleTimeout, slog.Default()).Run)
			slog.Info("idle-suspend sweeper started", "apps", n)
		}
	}

	// Add-on installer (D3) — only when the gate is open and the registry loaded.
	var addonInstaller *addon.Installer
	if cfg.ManagedServices && addonRegistry != nil {
		addonInstaller = addon.NewInstaller(st, box, addonRegistry, worker, dbProvisioner, slog.Default())
	}

	// DNS + cert status engine (plan 09 F3): an always-on, in-memory projection
	// of each managed host's DNS + TLS configuration status. Resolves via a
	// public recursive resolver (bypassing the box's local cache) and reuses the
	// shared cert probe. The proxy manager pushes route-failure errors into it.
	dnsResolver := os.Getenv("VAC_DNS_RESOLVER")
	if dnsResolver == "" {
		dnsResolver = "1.1.1.1:53"
	}
	statusEngine := domainstatus.New(domainstatus.Config{
		Source:    statusHostSource{store: st, proxy: proxyMgr},
		Resolver:  domainstatus.PublicResolver(dnsResolver),
		CertProbe: certprobe.New(certProbeAddr(cfg), 10*time.Second),
		VPSIP:     hostIP,
		Logger:    slog.Default(),
	})
	proxyMgr.SetStatusEngine(statusEngine)
	superviseDaemon(ctx, "domain-status-engine", statusEngine.Run)

	srv, err := server.New(ctx, cfg, st, worker, docker, proxyMgr, hub, statsMgr, notifier, backupEngine, backupRestorer, backupVerifier, jobsEngine, dbProvisioner, addonRegistry, addonInstaller, statusEngine, secPosture, secTraffic, secHost, previewSvc, waker, monitor)
	if err != nil {
		slog.Error("server init failed", "err", err)
		os.Exit(1)
	}

	go func() {
		slog.Info("vac-api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Notify that the control plane is back up — but not on a genuine first
	// boot (no admin yet), so a brand-new install doesn't ping a webhook the
	// operator only just configured via env.
	if n, err := st.CountUsers(ctx); err == nil && n > 0 {
		notifier.VACRestarted()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutdown signal received")

	// Drop all WebSocket subscribers first so long-lived stream handlers return
	// and the graceful HTTP shutdown below doesn't block on them.
	hub.Close()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	// Cancel pipeline context so the worker exits, then wait for it.
	cancel()
	worker.Wait()
	slog.Info("shutdown complete")
}

// loadCaddyBaseConfig installs VAC's base Caddy config and reconciles routes
// from the DB. Like the docker probe, failure is non-fatal — VAC must boot on
// a host where the proxy is briefly unreachable so the operator can recover;
// the next deploy (or a manual restart) re-pushes the config.
func loadCaddyBaseConfig(parent context.Context, cfg config.Config, client *caddy.Client, mgr *proxy.Manager) {
	askURL := os.Getenv("VAC_CADDY_ASK_URL")
	if askURL == "" {
		askURL = fmt.Sprintf("http://vac-api:%d/internal/caddy/ask", cfg.Server.Port)
	}
	// Caddy's on-demand permission module calls the ask endpoint with only
	// ?domain=<host> appended and cannot send custom headers, so the shared secret
	// rides along as a query param (Caddy preserves any query already on the
	// endpoint URL). caddy_ask.go enforces it. The wake path uses the
	// X-Caddy-Ask-Token header instead — that flows through reverse_proxy, where
	// the proxy manager can set headers.
	if cfg.CaddyAskToken != "" {
		if u, err := url.Parse(askURL); err == nil {
			q := u.Query()
			q.Set("token", cfg.CaddyAskToken)
			u.RawQuery = q.Encode()
			askURL = u.String()
		}
	}
	// Seed the base with any uploaded (bring-your-own) certs so they are served
	// immediately after a Caddy restart, before the first route sync re-pushes
	// them (dns-automation plan B). Best-effort: an error here just means the
	// next Sync/SyncCerts repopulates the cert set.
	var certs []caddy.CertKeyPair
	if c, err := mgr.DesiredCerts(parent); err != nil {
		slog.Warn("could not load uploaded certs for base config", "err", err)
	} else {
		certs = c
	}
	base := caddy.BaseConfig(caddy.BaseOptions{
		AdminListen:   ":2019",
		AccessLogPath: cfg.CaddyAccessLog,
		AskURL:        askURL,
		ACMECA:        cfg.ACMECA,
		Certs:         certs,
	})

	// Hand the same base config to the manager so it can self-heal whenever the
	// proxy restarts back to its admin-only bootstrap config (per-deploy and on
	// boot reconcile, via Manager.ensureBaseConfig).
	mgr.SetBaseConfig(base)

	// A just-started proxy can lose the boot race, so retry the initial Load with
	// capped exponential backoff (1s, 2s, 4s, 8s...) for up to ~30s. Each attempt
	// uses its own short timeout. Non-fatal: if every attempt fails, the
	// per-deploy ensureBaseConfig will recover once the proxy is reachable.
	const overallBudget = 30 * time.Second
	deadline := time.Now().Add(overallBudget)
	backoff := time.Second
	loaded := false
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		err := client.Load(ctx, base)
		cancel()
		if err == nil {
			loaded = true
			slog.Info("caddy base config loaded", "attempt", attempt)
			break
		}
		if time.Now().After(deadline) {
			slog.Warn("caddy base config load failed; routing will converge once the proxy is reachable", "err", err, "attempts", attempt)
			break
		}
		slog.Warn("caddy base config load failed; retrying", "err", err, "attempt", attempt, "backoff", backoff.String())
		select {
		case <-parent.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 8*time.Second {
			backoff *= 2
			if backoff > 8*time.Second {
				backoff = 8 * time.Second
			}
		}
	}
	if !loaded {
		return
	}
	if err := mgr.Reconcile(parent); err != nil {
		slog.Warn("caddy route reconcile reported errors", "err", err)
	}
}

// statusHostSource enumerates the hosts the status engine watches: every custom
// domain (store rows) plus every derived auto host (proxy manager). It is the
// single place the two are combined, matching how reconcile derives routes.
type statusHostSource struct {
	store interface {
		ListAllDomains(ctx context.Context) ([]store.Domain, error)
	}
	proxy interface {
		AutoHosts(ctx context.Context) ([]proxy.AutoHost, error)
	}
}

func (s statusHostSource) StatusHosts(ctx context.Context) ([]string, error) {
	domains, err := s.store.ListAllDomains(ctx)
	if err != nil {
		return nil, err
	}
	autos, err := s.proxy.AutoHosts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(domains)+len(autos))
	for _, d := range domains {
		out = append(out, d.Hostname)
	}
	for _, a := range autos {
		out = append(out, a.Hostname)
	}
	return out, nil
}

// certProbeAddr is the host:port the cert-expiry checker TLS-dials with each
// managed host's SNI. It defaults to "<caddy-admin-host>:443" — the proxy serves
// :443 on the same internal network the admin URL points at — unless overridden
// by VAC_CERT_PROBE_ADDR.
func certProbeAddr(cfg config.Config) string {
	if cfg.CertProbeAddr != "" {
		return cfg.CertProbeAddr
	}
	host := "vac-proxy"
	if u, err := url.Parse(cfg.CaddyAdminURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return net.JoinHostPort(host, "443")
}

// buildCacheMaxBytes resolves the nightly build-cache ceiling. The
// VAC_BUILD_CACHE toggle collapses into a single value the pruner understands:
// 0 (disabled, cache left to Docker's GC) when off, else BuildCacheMaxGB in
// bytes.
func buildCacheMaxBytes(cfg config.Config) int64 {
	if !cfg.BuildCache || cfg.BuildCacheMaxGB <= 0 {
		return 0
	}
	return int64(cfg.BuildCacheMaxGB) << 30
}

// probeDockerCLI runs `docker version` once at boot. Failure is logged but
// non-fatal — we want VAC to come up on a misconfigured host so the operator
// can fix the socket from the UI. Deployments will refuse to run until the
// probe succeeds at request time.
func probeDockerCLI(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	// Explicit minimal env — never inherit os.Environ, which would leak
	// VAC_MASTER_KEY into the child process.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if err != nil {
		slog.Warn("docker CLI probe failed; deployments will not run until the docker socket is reachable", "err", err)
		return
	}
	slog.Info("docker CLI probe ok", "server_version", strings.TrimSpace(string(out)))
}

func printFirstBootBanner(cfg config.Config, setupToken string) {
	bar := strings.Repeat("━", 64)
	var b strings.Builder
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, bar)
	fmt.Fprintln(&b, "  VAC — first boot")
	fmt.Fprintln(&b, bar)
	fmt.Fprintln(&b)
	if setupToken != "" {
		fmt.Fprintf(&b, "  Dashboard:  http://localhost:%d/setup?token=%s\n", cfg.Server.Port, setupToken)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "  Setup token (required to create the admin account):")
		fmt.Fprintln(&b, "    "+setupToken)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "  The token is also stored at:")
		fmt.Fprintln(&b, "    "+filepath.Join(cfg.WorkDir, "setup.token"))
	} else {
		fmt.Fprintf(&b, "  Dashboard:  http://localhost:%d\n", cfg.Server.Port)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "  ⚠  Could not create setup token — see logs.")
		fmt.Fprintln(&b, "     /api/setup/admin will refuse until this is resolved.")
	}
	if len(cfg.MasterKey) == 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "  ⚠  Set VAC_MASTER_KEY (32 bytes hex) in your")
		fmt.Fprintln(&b, "     environment before deploying any apps.")
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, bar)
	fmt.Print(b.String())
}

// controlDBName extracts the control-plane database name from VAC_DATABASE_URL so
// the box-wide inventory can pin it (plan 20). Defaults to "vac" when the URL is
// unparseable or carries no path.
func controlDBName(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "vac"
	}
	if name := strings.TrimPrefix(u.Path, "/"); name != "" {
		return name
	}
	return "vac"
}

// superviseDaemon runs a long-lived background loop that survives a panic. An
// unrecovered panic in any goroutine crashes the entire control plane, so every
// always-on daemon is launched through here: on panic it logs the stack and
// restarts the loop after a short backoff; on a clean return (the normal path is
// ctx cancellation) it exits. name labels the daemon in logs.
func superviseDaemon(ctx context.Context, name string, run func(context.Context)) {
	go func() {
		for ctx.Err() == nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("daemon panicked; restarting after backoff",
							"daemon", name, "panic", r, "stack", string(debug.Stack()))
					}
				}()
				run(ctx)
			}()
			// run returned: clean shutdown if ctx is done, else a recovered panic —
			// back off before relaunching so a tight panic loop can't spin the CPU.
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}
