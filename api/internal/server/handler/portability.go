package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/appspec"
	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/portability"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// maxImportBodyBytes caps the import body. A spec is small declarative YAML; this
// bounds memory against a hostile or accidental large upload.
const maxImportBodyBytes = 1 << 20 // 1 MiB

// ExportApp returns an app's portable vac.app.yaml (plan 18, format=spec). It is
// a read of configuration only — sensitive env values are omitted, so no secret
// is disclosed (unlike the future plaintext exit-ramp formats, which will need an
// audited mutating route). The spec is sent as a downloadable attachment.
func ExportApp(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		spec, err := portability.Export(r.Context(), s, box, id)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				WriteError(w, http.StatusNotFound, "app not found")
			case errors.Is(err, portability.ErrMasterKeyRequired):
				WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; env values cannot be decrypted for export")
			default:
				WriteError(w, http.StatusInternalServerError, "could not export app")
			}
			return
		}
		out, err := appspec.Marshal(spec)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not render spec")
			return
		}
		slog.Info("app exported", "app", id, "slug", spec.Metadata.Slug, "format", "spec")
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+spec.Metadata.Slug+`.vac.app.yaml"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	}
}

// ImportApp creates or updates an app from a posted vac.app.yaml, idempotent on
// slug. The body is the spec (YAML, or JSON — YAML is a superset). The audit
// middleware records the action; we enrich it with the target and a summary.
func ImportApp(s *store.Store, box *crypto.Box, syncer RouteSyncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBodyBytes))
		if err != nil {
			WriteError(w, http.StatusBadRequest, "could not read request body (too large?)")
			return
		}
		spec, err := appspec.Unmarshal(body)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid spec: "+err.Error())
			return
		}
		result, err := portability.Import(r.Context(), s, box, spec)
		if err != nil {
			var invalid portability.InvalidSpecError
			switch {
			case errors.As(err, &invalid):
				WriteError(w, http.StatusBadRequest, "invalid spec: "+invalid.Error())
			case errors.Is(err, portability.ErrMasterKeyRequired):
				WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; env values cannot be sealed on import")
			default:
				WriteError(w, http.StatusInternalServerError, "could not import app")
			}
			return
		}

		audit.SetTarget(r.Context(), "app", result.AppID)
		verb := "updated"
		if result.Created {
			verb = "imported"
		}
		audit.Describe(r.Context(), verb+" app "+result.Slug+" from spec")

		// Best-effort: converge Caddy now. The app isn't deployed yet, so its
		// routes have no upstream until the first deploy — the DB is the source of
		// truth and the deploy reconcile will finish the job either way.
		syncRoutes(r.Context(), syncer, result.AppID)

		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		WriteJSON(w, status, result)
	}
}
