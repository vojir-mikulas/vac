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
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/dockerevents"
	"github.com/vojir-mikulas/vac/api/internal/domainstatus"
	"github.com/vojir-mikulas/vac/api/internal/logstream"
	"github.com/vojir-mikulas/vac/api/internal/notify"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/reqmetrics"
	"github.com/vojir-mikulas/vac/api/internal/retention"
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
	}, slog.Default())

	// Apply any runtime base-domain override saved in instance_settings so it
	// survives restarts (the UI writes both the DB and the live manager).
	if settings, err := st.GetInstanceSettings(ctx); err != nil {
		slog.Warn("could not load instance settings; using config base domain", "err", err)
	} else if settings.BaseDomain != "" {
		proxyMgr.SetBaseDomain(settings.BaseDomain)
	}

	loadCaddyBaseConfig(ctx, cfg, caddyClient, proxyMgr)

	// Outbound notifications (Discord/Slack). Stored webhook URLs are decrypted
	// with the master key; VAC_NOTIFY_* env vars override them.
	var notifyBaseURL string
	if cfg.BaseDomain != "" {
		notifyBaseURL = "https://" + cfg.BaseDomain
	}
	notifier := notify.New(st, box, cfg.NotifyDiscordURL, cfg.NotifySlackURL, notifyBaseURL, slog.Default())

	pipeline := deploy.NewPipeline(st, keys, box, docker, cfg.WorkDir, cfg.HealthCheckTimeout, cfg.HealthCheckRetries, slog.Default())
	pipeline.Router = proxyMgr
	pipeline.Hub = hub
	pipeline.Notifier = notifier
	worker := deploy.NewPipelineWorker(pipeline, 0)
	worker.Start(ctx)

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
		}, notifier, slog.Default())
		secTraffic = secMonitor
	}
	secPosture := security.NewPosture(st, security.PostureConfig{
		Exposure:         cfg.Exposure,
		MasterKeyPresent: len(cfg.MasterKey) > 0,
		MetricsTokenSet:  cfg.MetricsToken != "",
		BaseDomainSet:    cfg.BaseDomain != "",
	})
	secHost := security.NewHost()

	// Request-rate metrics: tail Caddy's JSON access log and aggregate per
	// service into the rolling window. When the security monitor is on, it
	// observes each parsed line through the same tail.
	collector := reqmetrics.New(st, cfg.CaddyAccessLog, cfg.CaddyMetricsInterval, slog.Default())
	if secMonitor != nil {
		collector.SetObserver(secMonitor.Observe)
		go secMonitor.Run(ctx)
	}
	go collector.Run(ctx)

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
	go eventBus.Run(ctx)

	monitor := crashloop.New(eventBus, docker, st, crashloop.Config{
		Threshold: cfg.CrashLoopThreshold,
		Window:    cfg.CrashLoopWindow,
	}, slog.Default())
	monitor.SetNotifier(notifier)
	monitor.SetInspector(docker)
	go monitor.Run(ctx)

	// Runtime-log capture: one follower per running container, writing to the
	// DB ring buffer and teeing to the hub. Reconciles on deploy/lifecycle, on
	// container events, and on a periodic resync.
	logSup := logstream.New(docker, st, st, hub, eventBus, logstream.Config{
		RingBuffer: cfg.LogRingBuffer,
	}, slog.Default())
	go logSup.Run(ctx)
	pipeline.Reconciler = logSup

	pruner := retention.New(st, retention.Config{
		RuntimeDays:    cfg.LogRetentionDays,
		RequestMetrics: cfg.RequestMetricsRetention,
		ActivityDays:   cfg.ActivityRetentionDays,
		RingBuffer:     cfg.LogRingBuffer,
		HourOfDay:      3,
	}, slog.Default())
	go pruner.Run(ctx)

	// Cert-expiry notification (plan 03). Reads each managed host's real TLS
	// expiry by handshaking the proxy with the host's SNI, and alerts once when a
	// cert is within the window and hasn't auto-renewed.
	certChecker := certcheck.New(st, notifier, certProbeAddr(cfg), 10*time.Second, certcheck.Config{
		Threshold: time.Duration(cfg.CertExpiryDays) * 24 * time.Hour,
	}, slog.Default())
	go certChecker.Run(ctx)

	// Track D (managed services). The backup engine is always constructed so the
	// manual "Back up now" endpoint works the moment the flag is on; the
	// scheduler goroutine, however, only starts when VAC_MANAGED_SERVICES is on
	// AND at least one backup config exists — zero idle footprint otherwise
	// (decision #8). New configs added later are picked up on the next restart.
	backupEngine := backup.NewEngine(st, docker, box, cfg.WorkDir, notifier, slog.Default())
	var dbProvisioner *dbprovision.Provisioner
	if cfg.ManagedServices {
		if n, err := st.CountBackupConfigs(ctx); err != nil {
			slog.Warn("backup: could not count configs; scheduler not started", "err", err)
		} else if n > 0 {
			go backup.NewScheduler(st, backupEngine, slog.Default()).Run(ctx)
			slog.Info("backup scheduler started", "configs", n)
		}

		// Managed databases (D2). The provisioner is only built when the gate is
		// open — its engines hold docker/pool handles but start no goroutines.
		dbProvisioner = dbprovision.New(st, box, pool, docker, dbprovision.Config{
			WorkDir:     cfg.WorkDir,
			EdgeNetwork: cfg.EdgeNetwork,
			MasterKey:   cfg.MasterKey,
		}, slog.Default())
		if cfg.ManagedDBIsolated {
			slog.Warn("VAC_MANAGED_DB_ISOLATED is set, but isolated managed Postgres is not yet implemented; using shared vac-db (see docs/deviations.md)")
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
	go statusEngine.Run(ctx)

	srv, err := server.New(ctx, cfg, st, worker, docker, proxyMgr, hub, statsMgr, notifier, backupEngine, dbProvisioner, addonRegistry, addonInstaller, statusEngine, secPosture, secTraffic, secHost)
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
	base := caddy.BaseConfig(caddy.BaseOptions{
		AdminListen:   ":2019",
		AccessLogPath: cfg.CaddyAccessLog,
		AskURL:        askURL,
		ACMECA:        cfg.ACMECA,
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
