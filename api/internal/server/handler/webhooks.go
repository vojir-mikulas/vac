package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/preview"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/webhook"
)

// PreviewService is the slice of *preview.Service the webhook fork and the
// previews REST surface depend on. An interface so handler tests can fake it and
// so the surface degrades cleanly (nil) when previews aren't wired.
type PreviewService interface {
	EnsurePreview(ctx context.Context, parentID, branch string) error
	TeardownByBranch(ctx context.Context, parentID, branch string) error
	Teardown(ctx context.Context, previewID string) error
	MaxPreviews() int
}

// webhookSecretBytes is the entropy of a generated webhook secret (32 bytes →
// 64 hex chars). Plenty for an HMAC key / bearer token.
const webhookSecretBytes = 32

type webhookConfigDTO struct {
	URL        string `json:"url"`
	Configured bool   `json:"configured"`
}

// webhookURL builds the inbound endpoint an external Git host should call. It is
// derived from the request so it reflects however the dashboard is reached.
func webhookURL(r *http.Request, appID string) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/webhooks/" + appID
}

// GetAppWebhookConfig reports the inbound webhook URL and whether a secret is
// set (which is what enables the endpoint). The secret itself is never returned
// after creation.
func GetAppWebhookConfig(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		enc, err := s.GetAppWebhookSecret(r.Context(), appID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load webhook config")
			return
		}
		WriteJSON(w, http.StatusOK, webhookConfigDTO{
			URL:        webhookURL(r, appID),
			Configured: len(enc) > 0,
		})
	}
}

type regenerateWebhookResponse struct {
	URL    string `json:"url"`
	Secret string `json:"secret"` // shown once; never recoverable afterwards
}

// RegenerateAppWebhookSecret mints a fresh secret, seals it, and returns the
// plaintext once. Any previously-configured secret is invalidated.
func RegenerateAppWebhookSecret(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		if box == nil {
			WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; webhook secrets cannot be encrypted")
			return
		}
		raw := make([]byte, webhookSecretBytes)
		if _, err := rand.Read(raw); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not generate secret")
			return
		}
		secret := hex.EncodeToString(raw)
		sealed, err := box.Seal([]byte(secret))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not encrypt secret")
			return
		}
		// SetAppWebhookSecret returns ErrNotFound for an unknown app — no need to
		// pre-load it.
		if err := s.SetAppWebhookSecret(r.Context(), appID, sealed); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not save secret")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Action(r.Context(), "webhook.secret_regenerated", nil)
		WriteJSON(w, http.StatusCreated, regenerateWebhookResponse{
			URL:    webhookURL(r, appID),
			Secret: secret,
		})
	}
}

// DeleteAppWebhookSecret disables push-to-deploy by clearing the secret.
func DeleteAppWebhookSecret(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		if err := s.SetAppWebhookSecret(r.Context(), appID, nil); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not clear secret")
			return
		}
		audit.SetTarget(r.Context(), "app", appID)
		audit.Action(r.Context(), "webhook.disabled", nil)
		WriteJSON(w, http.StatusOK, map[string]int{"cleared": 1})
	}
}

// Webhook is the inbound push-to-deploy endpoint (POST /webhooks/{appID}). It is
// unauthenticated by design — it authenticates the *payload* against the app's
// secret (GitHub HMAC / GitLab token / generic token) rather than a session.
// It records its own audit rows (it runs outside the /api audit middleware) so
// matched deploys and ignored/coalesced pushes both show up in the activity log.
func Webhook(s *store.Store, box *crypto.Box, worker DeploymentEnqueuer, previews PreviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "appID")

		app, err := s.GetApp(r.Context(), appID)
		if err != nil {
			// Unknown app or no secret are reported the same way so the endpoint
			// doesn't reveal which apps exist.
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		enc, err := s.GetAppWebhookSecret(r.Context(), appID)
		if err != nil || len(enc) == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if box == nil {
			WriteError(w, http.StatusServiceUnavailable, "VAC_MASTER_KEY not configured")
			return
		}
		secret, err := box.Open(enc)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read webhook secret")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "could not read body")
			return
		}
		if err := webhook.Verify(secret, body, r); err != nil {
			WriteError(w, http.StatusUnauthorized, "invalid signature")
			return
		}

		ref, err := webhook.ExtractRef(r, body)
		if err != nil {
			// e.g. a GitHub ping — authenticated but nothing to deploy.
			auditWebhookEntry(s, r, appID, "webhook delivery with no ref ignored for "+app.Slug, http.StatusOK)
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no ref"})
			return
		}
		kind, name := webhook.ParseRef(ref)

		// Manual branch entry points validate against gitRefRe; the webhook ref is
		// equally attacker-influenceable (anyone who can push a branch / holds the
		// secret), and `name` flows into preview branch storage → git fetch. Reject
		// refs that don't match the accepted charset so a `-`-leading or otherwise
		// hostile ref can't reach git as an argument.
		if !gitRefRe.MatchString(name) {
			auditWebhookEntry(s, r, appID, "ignored "+kind+" with unsupported ref name for "+app.Slug, http.StatusOK)
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unsupported ref name"})
			return
		}

		triggers, err := s.ListDeployTriggers(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load triggers")
			return
		}

		// Preview-deployment fork (preview-deployments.md). A push that deleted a
		// branch reaps that branch's preview; a push to a non-default branch
		// matching a `preview` trigger creates-or-redeploys a preview app instead
		// of deploying the parent. Both run only when the lifecycle is wired.
		if previews != nil && kind == webhook.KindPush {
			if webhook.IsBranchDelete(body) {
				switch err := previews.TeardownByBranch(r.Context(), appID, name); {
				case err == nil:
					auditWebhookEntry(s, r, appID, "tore down preview for deleted branch "+name+" of "+app.Slug, http.StatusAccepted)
					WriteJSON(w, http.StatusAccepted, map[string]string{"status": "preview-teardown"})
				case errors.Is(err, store.ErrNotFound):
					auditWebhookEntry(s, r, appID, "ignored delete of "+name+" for "+app.Slug+" (no preview)", http.StatusOK)
					WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no preview for branch"})
				default:
					WriteError(w, http.StatusInternalServerError, "could not tear down preview")
				}
				return
			}
			// A push to the parent's tracked branch deploys the parent (the push
			// trigger below), never a preview; only other branches become previews.
			if name != app.GitBranch && webhook.MatchTriggers(triggers, store.TriggerEventPreview, name) {
				switch err := previews.EnsurePreview(r.Context(), appID, name); {
				case err == nil:
					auditWebhookEntry(s, r, appID, "preview deploy of "+app.Slug+" from branch "+name, http.StatusAccepted)
					WriteJSON(w, http.StatusAccepted, map[string]string{"status": "preview"})
				case errors.Is(err, preview.ErrCapReached):
					auditWebhookEntry(s, r, appID, "refused preview of "+app.Slug+" from "+name+" (preview limit reached)", http.StatusAccepted)
					WriteJSON(w, http.StatusAccepted, map[string]string{"status": "capped", "reason": "preview limit reached"})
				default:
					WriteError(w, http.StatusInternalServerError, "could not ensure preview")
				}
				return
			}
		}

		if !webhook.MatchTriggers(triggers, kind, name) {
			auditWebhookEntry(s, r, appID, "ignored "+kind+" "+name+" for "+app.Slug+" (no matching rule)", http.StatusOK)
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no matching trigger"})
			return
		}

		// Coalesce a burst of rapid pushes: while a build is in flight, don't
		// stack another behind it.
		if active, _ := s.HasActiveDeployment(r.Context(), appID); active {
			auditWebhookEntry(s, r, appID, "coalesced "+kind+" "+name+" for "+app.Slug+" (deploy already in progress)", http.StatusAccepted)
			WriteJSON(w, http.StatusAccepted, map[string]string{"status": "coalesced"})
			return
		}

		triggeredBy := store.TriggeredPush
		if kind == webhook.KindTag {
			triggeredBy = store.TriggeredTag
		}

		// Approval gate (maintenance-mode-and-deploy-gates.md, Phase 4): if the
		// matched trigger requires approval, park the deploy as `pending-approval`
		// (created but NOT enqueued) until an operator approves it. Checked before
		// the deploy window — an approval-gated push waits for a human regardless of
		// schedule, and the approve action enqueues immediately.
		if mt := webhook.MatchingTrigger(triggers, kind, name); mt != nil && mt.RequireApproval {
			_, cerr := s.CreateDeploymentWithStatus(r.Context(), appID, triggeredBy, deploy.DeploymentStatusPendingApproval, nil)
			if cerr != nil && !errors.Is(cerr, store.ErrActiveDeploymentExists) {
				WriteError(w, http.StatusInternalServerError, "could not create deployment")
				return
			}
			auditWebhookEntry(s, r, appID, "deploy of "+app.Slug+" from "+kind+" "+name+" awaiting approval", http.StatusAccepted)
			WriteJSON(w, http.StatusAccepted, map[string]string{"status": "pending-approval"})
			return
		}

		// Deploy window (maintenance-mode-and-deploy-gates.md, Phase 3): a push that
		// arrives outside every configured window is parked as `scheduled` rather
		// than dropped — the window sweeper releases it when a window opens. A
		// parse error fails open (treats it as always-allowed) so a corrupt window
		// can't block deploys outright.
		if windows, werr := webhook.ParseWindows(app.DeployWindow); werr == nil && !webhook.Allows(time.Now(), windows) {
			_, cerr := s.CreateDeploymentWithStatus(r.Context(), appID, triggeredBy, deploy.DeploymentStatusScheduled, nil)
			if cerr != nil && !errors.Is(cerr, store.ErrActiveDeploymentExists) {
				WriteError(w, http.StatusInternalServerError, "could not schedule deployment")
				return
			}
			auditWebhookEntry(s, r, appID, "scheduled "+kind+" "+name+" for "+app.Slug+" (outside deploy window)", http.StatusAccepted)
			WriteJSON(w, http.StatusAccepted, map[string]string{"status": "scheduled"})
			return
		}

		d, err := s.CreateDeployment(r.Context(), appID, triggeredBy, nil)
		if err != nil {
			// Lost the check-then-insert race against a concurrent trigger — the
			// per-app uniqueness guard caught it. Same outcome as the pre-check
			// above: coalesce rather than stack a second deploy.
			if errors.Is(err, store.ErrActiveDeploymentExists) {
				auditWebhookEntry(s, r, appID, "coalesced "+kind+" "+name+" for "+app.Slug+" (deploy already in progress)", http.StatusAccepted)
				WriteJSON(w, http.StatusAccepted, map[string]string{"status": "coalesced"})
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not create deployment")
			return
		}
		if err := worker.Enqueue(d.ID); err != nil {
			if errors.Is(err, deploy.ErrQueueFull) {
				WriteError(w, http.StatusServiceUnavailable, "deploy queue full — retry shortly")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not enqueue deployment")
			return
		}
		auditWebhookEntry(s, r, appID, "deploy of "+app.Slug+" from "+kind+" "+name, http.StatusAccepted)
		WriteJSON(w, http.StatusAccepted, map[string]string{"status": "deploying", "deployment_id": d.ID})
	}
}

// webhookAuditor is the store surface the webhook handler audits against. Kept
// narrow so the helper below reads clearly.
type webhookAuditor interface {
	InsertAuditLog(ctx context.Context, e store.AuditEntry) error
}

// auditWebhook records one system-attributed audit row for an inbound webhook.
// Best-effort: a failed insert must never fail the delivery. It runs on a
// detached context because the request may have already been answered.
func auditWebhookEntry(a webhookAuditor, r *http.Request, appID, summary string, status int) {
	appType := "app"
	entry := store.AuditEntry{
		ActorType:  store.ActorSystem,
		Action:     "POST /webhooks/{appID}",
		TargetType: &appType,
		TargetID:   &appID,
		Summary:    &summary,
		StatusCode: status,
	}
	if ip := webhookClientIP(r); ip != "" {
		entry.IP = &ip
	}
	if ua := r.UserAgent(); ua != "" {
		entry.UserAgent = &ua
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.InsertAuditLog(ctx, entry)
}

func webhookClientIP(r *http.Request) string {
	return ClientIPString(r)
}
