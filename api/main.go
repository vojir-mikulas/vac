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
	"strings"
	"syscall"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/caddy"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/crashloop"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/reqmetrics"
	"github.com/vojir-mikulas/vac/api/internal/retention"
	"github.com/vojir-mikulas/vac/api/internal/server"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func main() {
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
		printFirstBootBanner(cfg)
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

	// Phase 3: reverse proxy. The Caddy admin client + proxy manager drive
	// routing over the vac-edge network. The manager is the deploy pipeline's
	// Router (auto-domains + route sync + Caddy-gated health).
	caddyClient := caddy.New(cfg.CaddyAdminURL)
	proxyMgr := proxy.New(st, caddyClient, docker, proxy.Config{
		EdgeNetwork:    cfg.EdgeNetwork,
		BaseDomain:     cfg.BaseDomain,
		HealthInterval: 5 * time.Second,
		HealthTimeout:  cfg.HealthCheckTimeout,
		HealthRetries:  cfg.HealthCheckRetries,
	}, slog.Default())

	loadCaddyBaseConfig(ctx, cfg, caddyClient, proxyMgr)

	pipeline := deploy.NewPipeline(st, keys, box, docker, cfg.WorkDir, cfg.HealthCheckTimeout, cfg.HealthCheckRetries, slog.Default())
	pipeline.Router = proxyMgr
	worker := deploy.NewPipelineWorker(pipeline, 0)
	worker.Start(ctx)

	// Request-rate metrics: tail Caddy's JSON access log and aggregate per
	// service into the rolling window.
	collector := reqmetrics.New(st, cfg.CaddyAccessLog, cfg.CaddyMetricsInterval, slog.Default())
	go collector.Run(ctx)

	monitor := crashloop.New(docker, docker, st, crashloop.Config{
		Threshold: cfg.CrashLoopThreshold,
		Window:    cfg.CrashLoopWindow,
	}, slog.Default())
	go monitor.Run(ctx)

	pruner := retention.New(st, retention.Config{
		RuntimeDays:    cfg.LogRetentionDays,
		RequestMetrics: cfg.RequestMetricsRetention,
		HourOfDay:      3,
	}, slog.Default())
	go pruner.Run(ctx)

	srv := server.New(ctx, cfg, st, worker, docker, proxyMgr)

	go func() {
		slog.Info("vac-api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutdown signal received")

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

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := client.Load(ctx, base); err != nil {
		slog.Warn("caddy base config load failed; routing will converge once the proxy is reachable", "err", err)
		return
	}
	slog.Info("caddy base config loaded")
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

func printFirstBootBanner(cfg config.Config) {
	bar := strings.Repeat("━", 50)
	var b strings.Builder
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, bar)
	fmt.Fprintln(&b, "  VAC — first boot")
	fmt.Fprintln(&b, bar)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  Dashboard:  http://localhost:%d\n", cfg.Server.Port)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  Open the dashboard to create your admin account.")
	if len(cfg.MasterKey) == 0 {
		fmt.Fprintln(&b, "  ⚠  Set VAC_MASTER_KEY (32 bytes hex) in your")
		fmt.Fprintln(&b, "     environment before deploying any apps.")
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, bar)
	fmt.Print(b.String())
}
