// Package server wires the HTTP router and middleware stack.
package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ui"
)

func New(cfg config.Config, s *store.Store) *http.Server {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", handler.Health)

	r.Route("/api", func(r chi.Router) {
		r.Route("/setup", func(r chi.Router) {
			r.Get("/status", handler.SetupStatus(s))
			r.Post("/admin", handler.SetupAdmin(s))
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
