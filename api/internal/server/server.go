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
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/server/middleware"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ui"
)

// New wires the chi router and returns a configured *http.Server. ctx governs
// background goroutines (rate limit eviction) — cancel it on shutdown.
// `worker` may be nil in tests where the deployment surface is not exercised.
func New(ctx context.Context, cfg config.Config, s *store.Store, worker *deploy.Worker, docker *dockercli.Compose) *http.Server {
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
	tokm := auth.NewTokenManager(s)
	keys := sshkey.NewManager(s, box)

	// One shared limiter across the auth surface: an attacker who burns the
	// password budget should not then get a fresh budget on /auth/totp.
	authLimiter := middleware.NewRateLimiter(ctx, cfg.LoginRateLimit, cfg.LoginRateWindow)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Logger)
	r.Use(chimw.Timeout(60 * time.Second))

	r.Get("/health", handler.Health(s))

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.Auth(sm, tokm))
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
			r.Get("/auth/api-tokens", handler.ListAPITokens(s))
			r.Post("/auth/api-tokens", handler.CreateAPIToken(tokm))
			r.Delete("/auth/api-tokens/{id}", handler.RevokeAPIToken(tokm))

			r.Route("/apps", func(r chi.Router) {
				r.Get("/", handler.ListApps(s))
				r.Post("/", handler.CreateApp(s))
				r.Get("/{id}", handler.GetApp(s))
				r.Patch("/{id}", handler.UpdateApp(s))
				r.Delete("/{id}", handler.DeleteApp(s))

				r.Get("/{id}/ssh-key", handler.GetAppSSHKey(s, keys))
				r.Post("/{id}/ssh-key/regenerate", handler.RegenerateAppSSHKey(s, keys))
				r.Delete("/{id}/ssh-key", handler.DeleteAppSSHKey(keys))

				r.Post("/{id}/test-connection", handler.TestConnection(s, keys, nil))

				if worker != nil {
					r.Post("/{id}/deployments", handler.TriggerDeployment(s, worker))
					r.Get("/{id}/deployments", handler.ListDeployments(s))
					r.Get("/{id}/deployments/{did}", handler.GetDeployment(s))
					r.Get("/{id}/deployments/{did}/logs", handler.GetDeploymentLogs(s))
				}

				r.Get("/{id}/env", handler.ListAppEnv(s))
				r.Put("/{id}/env", handler.ReplaceAppEnv(s, box))

				r.Get("/{id}/services", handler.ListAppServices(s))
				r.Patch("/{id}/services/{name}", handler.PatchAppService(s))

				if docker != nil {
					r.Post("/{id}/start", handler.StartApp(s, docker))
					r.Post("/{id}/stop", handler.StopApp(s, docker))
					r.Post("/{id}/restart", handler.RestartApp(s, docker))
					r.Post("/{id}/services/{name}/restart", handler.RestartService(s, docker))
				}
			})
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
