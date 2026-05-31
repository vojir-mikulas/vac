package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

type envVarKeyDTO struct {
	Key string `json:"key"`
}

// ListAppEnv returns the keys (no values) of an app's env vars. Values are
// only exposable through a reveal flow (out of scope for M11) — the UI
// renders ●●●● for each row.
func ListAppEnv(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rows, err := s.ListEnvVarsForApp(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list env vars")
			return
		}
		out := make([]envVarKeyDTO, 0, len(rows))
		for _, v := range rows {
			out = append(out, envVarKeyDTO{Key: v.Key})
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

type putEnvRequest struct {
	Vars map[string]string `json:"vars"`
}

// ReplaceAppEnv replaces the full set of env vars for an app. Body must be
// `{"vars": {"KEY": "value", ...}}`. Each value is sealed with VAC_MASTER_KEY
// before persistence; a missing master key returns 503.
func ReplaceAppEnv(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if box == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; env vars cannot be encrypted")
			return
		}
		var req putEnvRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if _, err := s.GetApp(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		inputs := make([]store.EnvVarInput, 0, len(req.Vars))
		for k, v := range req.Vars {
			k = strings.TrimSpace(k)
			if !validEnvKey(k) {
				WriteError(w, http.StatusBadRequest, "invalid env key: "+k)
				return
			}
			if !validEnvValue(v) {
				WriteError(w, http.StatusBadRequest, "env value for "+k+" contains forbidden characters (newline or NUL)")
				return
			}
			sealed, err := box.Seal([]byte(v))
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "could not encrypt env var")
				return
			}
			inputs = append(inputs, store.EnvVarInput{Key: k, Value: sealed})
		}
		if err := s.ReplaceEnvVars(r.Context(), id, inputs); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save env vars")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int{"saved": len(inputs)})
	}
}

// validEnvKey enforces the POSIX-style env-var name rules: first char letter
// or underscore, remaining chars letters, digits, or underscore.
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		isAlpha := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isAlpha {
			return false
		}
		if !isAlpha && !isDigit {
			return false
		}
	}
	return true
}

// validEnvValue rejects characters that would corrupt the rendered .env file
// or carry security-relevant payloads: newline / carriage-return (would let an
// attacker inject a second VAR=value pair) and NUL (process-exec boundary).
// envfile.go does escape these, but defence-in-depth: refuse them at the API
// boundary so they can never reach the disk in the first place.
func validEnvValue(v string) bool {
	return !strings.ContainsAny(v, "\n\r\x00")
}
