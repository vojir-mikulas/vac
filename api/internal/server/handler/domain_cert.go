package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/certupload"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// CertSyncer pushes VAC's uploaded-cert set to Caddy. *proxy.Manager satisfies
// it. The cert handlers call it best-effort after a DB change — a failure never
// fails the request; the boot reconcile (or next deploy) re-pushes the set.
type CertSyncer interface {
	SyncCerts(ctx context.Context) error
}

// certMetaDTO is the parsed certificate metadata returned after an upload so the
// UI can confirm what the operator installed.
type certMetaDTO struct {
	Subject    string     `json:"subject,omitempty"`
	DNSNames   []string   `json:"dns_names,omitempty"`
	NotBefore  time.Time  `json:"not_before"`
	NotAfter   time.Time  `json:"not_after"`
	Issuer     string     `json:"issuer,omitempty"`
	SelfSigned bool       `json:"self_signed"`
	Source     string     `json:"tls_cert_source"`
	UploadedAt *time.Time `json:"tls_cert_uploaded_at,omitempty"`
}

type uploadCertRequest struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// UploadDomainCert validates an operator-supplied cert+key, seals the private
// key, and stores it so Caddy serves it for the domain instead of ACME-issuing
// (bring-your-own cert, dns-automation plan B). Mounted behind RequireStepUp —
// installing the cert a host serves is a sensitive action, like the other
// destructive routes. Returns the parsed cert metadata.
func UploadDomainCert(s *store.Store, box *crypto.Box, certs CertSyncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if box == nil {
			WriteError(w, http.StatusServiceUnavailable, "encryption is not configured (VAC_MASTER_KEY); cannot store a certificate key")
			return
		}
		id := chi.URLParam(r, "id")
		d, err := s.GetDomainByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load domain")
			return
		}

		var req uploadCertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		certPEM := []byte(req.CertPEM)
		keyPEM := []byte(req.KeyPEM)
		if !certupload.FirstCertBlock(certPEM) {
			WriteError(w, http.StatusBadRequest, "cert_pem must contain a PEM-encoded certificate")
			return
		}

		meta, err := certupload.Validate(certPEM, keyPEM, d.Hostname)
		if err != nil {
			// All certupload validation failures are client errors (bad upload).
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}

		keyEnc, err := box.Seal(keyPEM)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not seal certificate key")
			return
		}
		uploadedAt := time.Now()
		if err := s.SetDomainCert(r.Context(), id, req.CertPEM, keyEnc, uploadedAt); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not store certificate")
			return
		}

		syncCerts(r.Context(), certs)
		audit.Describe(r.Context(), "uploaded a TLS certificate for "+d.Hostname)
		WriteJSON(w, http.StatusOK, certMetaDTO{
			Subject:    meta.Subject,
			DNSNames:   meta.DNSNames,
			NotBefore:  meta.NotBefore,
			NotAfter:   meta.NotAfter,
			Issuer:     meta.Issuer,
			SelfSigned: meta.SelfSigned,
			Source:     "uploaded",
			UploadedAt: &uploadedAt,
		})
	}
}

// ClearDomainCert removes an uploaded cert and reverts the domain to ACME, so
// Caddy resumes automatic issuance. Step-up gated like the upload.
func ClearDomainCert(s *store.Store, certs CertSyncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		d, err := s.GetDomainByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load domain")
			return
		}
		if err := s.ClearDomainCert(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "domain not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not clear certificate")
			return
		}
		syncCerts(r.Context(), certs)
		audit.Describe(r.Context(), "removed the uploaded TLS certificate for "+d.Hostname+" (reverted to ACME)")
		WriteJSON(w, http.StatusOK, map[string]string{"status": "cleared", "tls_cert_source": "acme"})
	}
}

// syncCerts runs a best-effort Caddy cert push. A failure never fails the
// request — the DB is the source of truth and a later sync converges Caddy.
func syncCerts(ctx context.Context, certs CertSyncer) {
	if certs == nil {
		return
	}
	if err := certs.SyncCerts(ctx); err != nil {
		slog.Warn("proxy cert sync after cert change failed", "err", err)
	}
}
