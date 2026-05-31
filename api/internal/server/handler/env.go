package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// envVarDTO is the read shape. `Value` is populated only for non-sensitive
// keys; sensitive keys omit it (revealed on demand via the reveal endpoint).
type envVarDTO struct {
	Key       string `json:"key"`
	Sensitive bool   `json:"sensitive"`
	Value     string `json:"value,omitempty"`
}

// ListAppEnv returns each env var's key + sensitivity. For non-sensitive keys
// it also returns the decrypted value so the UI can display/edit it inline;
// sensitive keys omit the value and require an explicit reveal. Every row is
// sealed at rest regardless (see docs/deviations.md D9), so decryption needs
// the master key — a missing key returns 503.
func ListAppEnv(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rows, err := s.ListEnvVarsForApp(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list env vars")
			return
		}
		out := make([]envVarDTO, 0, len(rows))
		for _, v := range rows {
			dto := envVarDTO{Key: v.Key, Sensitive: v.Sensitive}
			if !v.Sensitive {
				if box == nil {
					WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; env vars cannot be decrypted")
					return
				}
				plain, err := box.Open(v.Value)
				if err != nil {
					WriteError(w, http.StatusInternalServerError, "could not decrypt env var")
					return
				}
				dto.Value = string(plain)
			}
			out = append(out, dto)
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

// RevealAppEnv returns the decrypted value of a single sensitive key, backing
// the eye-toggle in the UI. Non-sensitive keys already ship their value via
// list, but revealing them is harmless and supported for uniformity.
func RevealAppEnv(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		key := chi.URLParam(r, "key")
		if box == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; env vars cannot be decrypted")
			return
		}
		v, err := s.GetEnvVar(r.Context(), id, key)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "env var not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load env var")
			return
		}
		plain, err := box.Open(v.Value)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not decrypt env var")
			return
		}
		// Audit: a sensitive value was disclosed. Keep the value itself out of logs.
		slog.Info("env var revealed", "app", id, "key", key, "sensitive", v.Sensitive)
		WriteJSON(w, http.StatusOK, envVarDTO{Key: v.Key, Sensitive: v.Sensitive, Value: string(plain)})
	}
}

type putEnvEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
}

type putEnvRequest struct {
	Vars []putEnvEntry `json:"vars"`
}

// ReplaceAppEnv replaces the full set of env vars for an app. Body must be
// `{"vars": [{"key","value","sensitive"}, ...]}`. Every value is sealed with
// VAC_MASTER_KEY before persistence regardless of `sensitive`; the flag only
// governs read-back. A missing master key returns 503.
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
		seen := make(map[string]struct{}, len(req.Vars))
		for _, e := range req.Vars {
			k := strings.TrimSpace(e.Key)
			if !validEnvKey(k) {
				WriteError(w, http.StatusBadRequest, "invalid env key: "+k)
				return
			}
			if _, dup := seen[k]; dup {
				WriteError(w, http.StatusBadRequest, "duplicate env key: "+k)
				return
			}
			seen[k] = struct{}{}
			if !validEnvValue(e.Value) {
				WriteError(w, http.StatusBadRequest, "env value for "+k+" contains forbidden characters (newline or NUL)")
				return
			}
			sealed, err := box.Seal([]byte(e.Value))
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "could not encrypt env var")
				return
			}
			inputs = append(inputs, store.EnvVarInput{Key: k, Value: sealed, Sensitive: e.Sensitive})
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
