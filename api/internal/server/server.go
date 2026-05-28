// Package server wires the HTTP router and middleware stack.
package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/server/middleware"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ui"
)

func New(cfg config.Config, s *store.Store) *http.Server {
	sm := auth.NewSessionManager(s, cfg.SessionTTL, cfg.SessionTTLExtended)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Logger)
	r.Use(chimw.Timeout(60 * time.Second))

	r.Get("/health", handler.Health)

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.Auth(sm))
		r.Use(middleware.CSRF)

		// Public — no session required.
		r.Route("/setup", func(r chi.Router) {
			r.Get("/status", handler.SetupStatus(s))
			r.Post("/admin", handler.SetupAdmin(s))
		})
		r.Post("/auth/login", handler.Login(s, sm, cfg))

		// Authenticated — requires a valid session cookie.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireSession)
			r.Post("/auth/logout", handler.Logout(sm, cfg))
			r.Get("/auth/me", handler.Me)
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
