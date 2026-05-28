// Package server wires the HTTP router and middleware stack.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/server/middleware"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ui"
)

// New wires the chi router and returns a configured *http.Server. ctx governs
// background goroutines (rate limit eviction) — cancel it on shutdown.
func New(ctx context.Context, cfg config.Config, s *store.Store) *http.Server {
	sm := auth.NewSessionManager(s, cfg.SessionTTL, cfg.SessionTTLExtended)

	// box may be nil when VAC_MASTER_KEY is unset — TOTP setup will then
	// refuse with a clear error rather than silently doing nothing.
	var box *crypto.Box
	if len(cfg.MasterKey) > 0 {
		b, err := crypto.New(cfg.MasterKey)
		if err != nil {
			slog.Warn("crypto box init failed; totp setup disabled", "err", err)
		} else {
			box = b
		}
	}
	tm := auth.NewTOTPManager(s, box)

	// One shared limiter across the auth surface: an attacker who burns the
	// password budget should not then get a fresh budget on /auth/totp.
	authLimiter := middleware.NewRateLimiter(ctx, cfg.LoginRateLimit, cfg.LoginRateWindow)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Logger)
	r.Use(chimw.Timeout(60 * time.Second))

	r.Get("/health", handler.Health)

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.Auth(sm))
		r.Use(middleware.CSRF)

		// Public — no session required. Setup-admin and the login endpoints
		// are the brute-force surface, so they sit behind the rate limiter.
		r.Route("/setup", func(r chi.Router) {
			r.Get("/status", handler.SetupStatus(s))
			r.With(authLimiter.Middleware).Post("/admin", handler.SetupAdmin(s))
		})
		r.With(authLimiter.Middleware).Post("/auth/login", handler.Login(s, sm, cfg))
		// TOTP login step is reached via the pre-auth cookie, not a full
		// session — so it sits outside the RequireSession group.
		r.With(authLimiter.Middleware).Post("/auth/totp", handler.TOTPLogin(s, sm, tm, cfg))

		// Authenticated — requires a valid session cookie.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireSession)
			r.Post("/auth/logout", handler.Logout(sm, cfg))
			r.Get("/auth/me", handler.Me)
			r.Post("/auth/totp/setup", handler.TOTPSetup(tm))
			r.Post("/auth/totp/enable", handler.TOTPEnable(tm))
			r.Delete("/auth/totp", handler.TOTPDisable(s, tm))
			r.Get("/auth/sessions", handler.ListSessions(s))
			r.Delete("/auth/sessions", handler.RevokeOtherSessions(s))
			r.Delete("/auth/sessions/{id}", handler.RevokeSession(s, sm))
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			handler.WriteError(w, http.StatusNotFound, "not found")
		})
	})

	r.Handle("/*", ui.Handler())

	return &http.Server{
		Addr:              cfg.Addr(),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
