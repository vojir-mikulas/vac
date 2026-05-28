package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/server"
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

	srv := server.New(ctx, cfg, st)

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
	slog.Info("shutdown complete")
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
