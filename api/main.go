package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/admin"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/caddy"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/crashloop"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/dockerevents"
	"github.com/vojir-mikulas/vac/api/internal/logstream"
	"github.com/vojir-mikulas/vac/api/internal/notify"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/reqmetrics"
	"github.com/vojir-mikulas/vac/api/internal/retention"
	"github.com/vojir-mikulas/vac/api/internal/server"
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

	// Request-rate metrics: tail Caddy's JSON access log and aggregate per
	// service into the rolling window.
	collector := reqmetrics.New(st, cfg.CaddyAccessLog, cfg.CaddyMetricsInterval, slog.Default())
	go collector.Run(ctx)

	// Real-time stats: per-service collectors (subscriber-gated via the hub's
	// subscribe hooks) plus host vitals. The host request-rate field reuses the
	// Caddy /metrics scrape.
	scraper := reqmetrics.NewScraper(strings.TrimRight(cfg.CaddyAdminURL, "/")+"/metrics", nil)
	hostCollector := stats.NewHostCollector(scraper, cfg.WorkDir)
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
		RingBuffer:     cfg.LogRingBuffer,
		HourOfDay:      3,
	}, slog.Default())
	go pruner.Run(ctx)

	srv, err := server.New(ctx, cfg, st, worker, docker, proxyMgr, hub, statsMgr, notifier)
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
