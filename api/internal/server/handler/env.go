package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// envVarDTO is the read shape. `Value` is populated only for non-sensitive
// keys; sensitive keys omit it (revealed on demand via the reveal endpoint).
type envVarDTO struct {
	Key       string `json:"key"`
	Sensitive bool   `json:"sensitive"`
	WriteOnly bool   `json:"write_only"`
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
			dto := envVarDTO{Key: v.Key, Sensitive: v.Sensitive, WriteOnly: v.WriteOnly}
			if !v.Sensitive && !v.WriteOnly {
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
		// A write-only secret is a one-way latch — refuse to disclose it before
		// any decryption happens. A refused reveal is not a disclosure, so it is
		// not audit-logged (unlike the success path below).
		if v.WriteOnly {
			WriteError(w, http.StatusForbidden, "this secret is write-only and cannot be revealed")
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
	// WriteOnly marks this key as unrevealable (implies Sensitive). Once
	// persisted write-only it cannot be downgraded — delete + recreate to change.
	WriteOnly bool `json:"write_only"`
	// Keep reuses the existing sealed value for this key instead of re-sealing
	// Value. It is the only way a never-revealable write-only secret survives a
	// full-replace PUT: the UI has no plaintext to re-send, so it asks the server
	// to carry the prior sealed bytes forward without ever decrypting them.
	Keep bool `json:"keep"`
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
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		// Load the prior set once: it backs both the curated-revert snapshot and the
		// write-only keep/downgrade logic below. priorByKey is keyed on env key.
		priorByKey := map[string]store.EnvVar{}
		if prior, err := s.ListEnvVarsForApp(r.Context(), id); err == nil {
			for _, v := range prior {
				priorByKey[v.Key] = v
			}
			// Curated-revert snapshot: capture the prior env set (sealed values, never
			// plaintext — the audit_log JSONB is not encrypted) so this replace can be
			// undone. Best-effort: a snapshot failure must not block the save.
			audit.Snapshot(r.Context(), map[string]any{"env": envSnapshot(prior)})
		}
		audit.SetTarget(r.Context(), "app", id)
		audit.Describe(r.Context(), "replaced environment for "+app.Slug)
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
			// Write-only is the stronger form; normalize so the list-omission and
			// reveal-refusal logic stays a single rule (write_only ⇒ sensitive).
			if e.WriteOnly {
				e.Sensitive = true
			}
			// One-way latch: a key already persisted write-only cannot be downgraded
			// (that would let someone clear the flag, then reveal). Delete + recreate.
			if prev, ok := priorByKey[k]; ok && prev.WriteOnly && !e.WriteOnly {
				WriteError(w, http.StatusBadRequest, "cannot downgrade a write-only secret; delete and recreate it")
				return
			}
			// Keep path: reuse the prior sealed bytes without decrypting. This is how
			// a never-revealable write-only secret the operator never touched survives
			// a full-replace PUT (the UI has no plaintext to re-send for it).
			if e.Keep {
				prev, ok := priorByKey[k]
				if !ok {
					WriteError(w, http.StatusBadRequest, "keep set for unknown key: "+k)
					return
				}
				inputs = append(inputs, store.EnvVarInput{Key: k, Value: prev.Value, Sensitive: e.Sensitive, WriteOnly: e.WriteOnly})
				continue
			}
			if !validEnvValue(e.Value) {
				WriteError(w, http.StatusBadRequest, "env value for "+k+" contains forbidden characters (newline or NUL)")
				return
			}
			sealed, err := box.Seal([]byte(e.Value))
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "could not encrypt env var")
				return
			}
			inputs = append(inputs, store.EnvVarInput{Key: k, Value: sealed, Sensitive: e.Sensitive, WriteOnly: e.WriteOnly})
		}
		if err := s.ReplaceEnvVars(r.Context(), id, inputs); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save env vars")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int{"saved": len(inputs)})
	}
}

// envVarSnap is the per-row shape stored in the revert before-snapshot. Value is
// base64 of the *sealed* bytes — the audit_log is not encrypted, so plaintext
// must never land here; the reverter feeds the sealed bytes straight back into
// ReplaceEnvVars without ever decrypting. Mirrors revert.envEntrySnap.
type envVarSnap struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
	WriteOnly bool   `json:"write_only"`
}

func envSnapshot(rows []store.EnvVar) []envVarSnap {
	out := make([]envVarSnap, 0, len(rows))
	for _, v := range rows {
		out = append(out, envVarSnap{
			Key:       v.Key,
			Value:     base64.StdEncoding.EncodeToString(v.Value),
			Sensitive: v.Sensitive,
			WriteOnly: v.WriteOnly,
		})
	}
	return out
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
