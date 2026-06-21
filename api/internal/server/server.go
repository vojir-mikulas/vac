// Package server wires the HTTP router and middleware stack.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/vojir-mikulas/vac/api/internal/addon"
	"github.com/vojir-mikulas/vac/api/internal/auditdiff"
	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dnsprovider"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/domainstatus"
	"github.com/vojir-mikulas/vac/api/internal/preview"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/revert"
	"github.com/vojir-mikulas/vac/api/internal/selfupdate"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
	"github.com/vojir-mikulas/vac/api/internal/server/middleware"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ui"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

// New wires the chi router and returns a configured *http.Server. ctx governs
// background goroutines (rate limit eviction) — cancel it on shutdown.
// `worker` and `pm` may be nil in tests where the deployment / proxy surface is
// not exercised.
func New(ctx context.Context, cfg config.Config, s *store.Store, worker *deploy.Worker, docker *dockercli.Compose, pm *proxy.Manager, hub *ws.Hub, statsProv handler.StatsProvider, notifier handler.TestSender, backupEngine handler.BackupRunner, backupRestorer handler.BackupRestorer, backupVerifier handler.BackupVerifier, jobsEngine handler.JobRunner, dbProv *dbprovision.Provisioner, addonCat *addon.Registry, addonInstaller *addon.Installer, dstatus *domainstatus.Engine, secPosture handler.SecurityPosture, secTraffic handler.SecurityTraffic, secHost handler.SecurityHost, previewSvc *preview.Service, waker handler.Waker, crashReset handler.CrashLoopResetter) (*http.Server, error) {
	// Gate trust of the proxy forwarding headers (X-Forwarded-Proto for the cookie
	// Secure decision, X-Forwarded-For for the client IP) on config — the bundled
	// vac-proxy sets them; a raw-HTTP box can disable trusting them.
	handler.SetTrustProxyHeaders(cfg.TrustProxyHeaders)

	// Convert the concrete (possibly-nil) manager into nil-able interface
	// values so handlers' `== nil` guards behave (a typed-nil pointer in an
	// interface is not nil).
	var (
		proxyMgr   handler.ProxyManager
		syncer     handler.RouteSyncer
		caddyPin   handler.CaddyPinger
		ctrlChk    handler.ControlDomainChecker
		autoChk    handler.AutoHostChecker
		autoList   handler.AutoHostLister
		baseDom    handler.BaseDomainSetter
		certSyncer handler.CertSyncer
	)
	if pm != nil {
		proxyMgr = pm
		syncer = pm
		caddyPin = pm
		ctrlChk = pm
		autoChk = pm
		autoList = pm
		baseDom = pm
		certSyncer = pm
	}
	var domStatus handler.DomainStatusProvider
	if dstatus != nil {
		domStatus = dstatus
	}
	// Preview lifecycle (preview-deployments.md): nil-able so the webhook fork and
	// the previews REST surface degrade cleanly when it isn't wired (tests).
	var previews handler.PreviewService
	if previewSvc != nil {
		previews = previewSvc
	}
	var routeRec handler.RouteReconciler
	if pm != nil {
		routeRec = pm
	}
	// Scale-to-zero wake handler resolves a suspended host back to its app via the
	// proxy manager. nil-able so the endpoint 404s cleanly when the proxy isn't
	// wired (tests).
	var wakeResolver handler.WakeResolver
	if pm != nil {
		wakeResolver = pm
	}

	// Same resolver the status engine uses, so the one-shot DNS-check button and
	// the background projection agree (plan 09 F3 §2): a public recursive resolver
	// that bypasses the box's local cache. VAC_DNS_RESOLVER="" keeps the system
	// resolver (egress-blocked fallback).
	dnsResolver := domainstatus.PublicResolver(dnsResolverAddr())

	// Managed-database surface (Track D / D2). Convert the possibly-nil concrete
	// provisioner into nil-able interfaces so the handlers' nil guards behave.
	var (
		dbHandler handler.DBProvisioner
		dbDeprov  handler.AppDBDeprovisioner
	)
	if dbProv != nil {
		dbHandler = dbProv
		dbDeprov = dbProv
	}

	// Add-on catalog (Track D / D3). nil-able interfaces for the handlers' guards.
	var (
		addonCatalog    handler.AddonCatalog
		addonInstaller2 handler.AddonInstaller
	)
	if addonCat != nil {
		addonCatalog = addonCat
	}
	if addonInstaller != nil {
		addonInstaller2 = addonInstaller
	}

	// VPS public address for the DNS-setup guidance and sidebar host row.
	hostIP := cfg.PublicIPAddr()

	wsOpts := wsAcceptOptions(cfg)

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

	// Custom-domain DNS automation (dns-automation plan A). Opt-in via
	// VAC_DNS_AUTOMATION; when off the automator is nil and the domain hook /
	// settings handlers degrade to the manual-record flow. The box is passed as a
	// nil interface when absent so the automator's nil-key guard behaves.
	var dnsAutomator handler.DNSAutomator
	if cfg.DNSAutomation {
		var opener dnsprovider.KeyOpener
		if box != nil {
			opener = box
		}
		dnsAutomator = dnsprovider.NewAutomator(true, s, opener, hostIP, slog.Default())
	}

	// One shared limiter across the auth surface: an attacker who burns the
	// password budget should not then get a fresh budget on /auth/totp.
	authLimiter := middleware.NewRateLimiter(ctx, cfg.LoginRateLimit, cfg.LoginRateWindow)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	// Lift the Caddy ask token from the query string into the header BEFORE the
	// request logger runs, so the shared secret never lands in access logs. Caddy's
	// on-demand module can't set headers, so it passes the secret as ?token=; this
	// hands it to the handler via the header it already accepts and scrubs the URL.
	r.Use(scrubCaddyAskToken)
	r.Use(chimw.Logger)
	// Per-request timeout that deliberately skips WebSocket upgrades — chi's
	// stock Timeout would cancel the long-lived WS streams (logs/stats/deploys/
	// exec) once the budget elapsed. See middleware.Timeout.
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(middleware.SecurityHeaders)

	r.Get("/health", handler.Health(s, caddyPin))

	// Inbound push-to-deploy webhook (plan 01). Unauthenticated by design — it
	// authenticates the payload against the app's secret (HMAC / token), not a
	// session — so it lives OUTSIDE the /api Auth+CSRF group. Body-limited like
	// the API surface. Mounted only when the deploy worker is wired.
	if worker != nil {
		// Per-IP limiter, separate from the auth bucket: the webhook is
		// unauthenticated, so without it an attacker can drive unbounded
		// deploy-enqueue / HMAC-compute attempts. Budget is generous (per-push,
		// legit Git hosts won't trip it) — see WebhookRateLimit defaults.
		webhookLimiter := middleware.NewRateLimiter(ctx, cfg.WebhookRateLimit, cfg.WebhookRateWindow)
		r.With(middleware.BodyLimit(middleware.MaxBodyBytes), webhookLimiter.Middleware).
			Post("/webhooks/{appID}", handler.Webhook(s, box, worker, previews))
	}

	// On-demand-TLS ask hook for Caddy. Unauthenticated by design (Caddy can't
	// present a session); reachable only on the internal compose network.
	r.Get("/internal/caddy/ask", handler.CaddyAsk(s, cfg.CaddyAskToken, ctrlChk, autoChk))

	// Scale-to-zero wake interceptor (docs/plans/scale-to-zero.md). A suspended
	// app's Caddy routes rewrite any path to this endpoint; it starts the stack
	// and redirects back. Unauthenticated like CaddyAsk (it's the route's job to
	// authenticate, via the shared X-Caddy-Ask-Token), outside the /api group, and
	// matched on every method so a rewritten POST still reaches it.
	r.HandleFunc("/__vac_wake", handler.WakeApp(waker, wakeResolver, cfg.CaddyAskToken))

	// Token-gated runtime introspection for the RAM benchmark (plan 07).
	// Default-closed: with VAC_METRICS_TOKEN unset these 404. Sits outside the
	// /api session group because a scraper / the bench harness present a bearer
	// token, not a session cookie + CSRF.
	r.Group(func(r chi.Router) {
		r.Use(middleware.MetricsToken(cfg.MetricsToken))
		r.Handle("/debug/vars", handler.DebugVars())
		r.Get("/debug/gc", handler.ForceGC())
		// Prometheus exposition (plan 13). Needs the stats surface for host +
		// per-service gauges; omitted in tests that pass a nil provider.
		if statsProv != nil {
			r.Get("/metrics", handler.MetricsExposition(statsProv, s, cfg.RequestMetricsRetention, cfg.Version, cfg.Commit))
		}
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.BodyLimit(middleware.MaxBodyBytes))
		r.Use(middleware.Auth(sm, tokm))
		r.Use(middleware.CSRF)
		// Innermost: the actor is resolved (Auth) and CSRF has passed, so every
		// mutating request that reaches a handler is audited with its outcome.
		r.Use(middleware.Audit(ctx, s))

		// Public — no session required. Setup-admin and the login endpoints
		// are the brute-force surface, so they sit behind the rate limiter.
		r.Route("/setup", func(r chi.Router) {
			r.Get("/status", handler.SetupStatus(s, cfg.WorkDir))
			r.With(authLimiter.Middleware).Post("/admin", handler.SetupAdmin(s, sm, cfg))
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
			// Step-up: re-prove 2FA on a live session to unlock destructive
			// actions for auth.StepUpTTL. Shares the auth rate-limit bucket so
			// codes can't be brute-forced.
			r.With(authLimiter.Middleware).Post("/auth/step-up", handler.StepUp(sm, tm))
			// Disabling 2FA is itself a sensitive action — gate it on a fresh
			// step-up in addition to the password re-check the handler enforces.
			r.With(middleware.RequireStepUp).Delete("/auth/totp", handler.TOTPDisable(s, tm))
			r.Get("/auth/sessions", handler.ListSessions(s))
			r.Delete("/auth/sessions", handler.RevokeOtherSessions(s))
			r.Delete("/auth/sessions/{id}", handler.RevokeSession(s, sm))
			r.Get("/auth/api-tokens", handler.ListAPITokens(s))
			r.Post("/auth/api-tokens", handler.CreateAPIToken(tokm))
			r.Delete("/auth/api-tokens/{id}", handler.RevokeAPIToken(tokm))

			// Activity feed (plan 11, Part 1) + curated revert (Part 2). The feed
			// is the durable successor to the deployment-derived activity view;
			// revert reapplies the before-snapshot of the safely-invertible set.
			r.Get("/audit", handler.ListAudit(s))
			r.Post("/audit/{id}/revert", handler.RevertAudit(revert.New(s, baseDom)))
			// Change preview (plan 22): a sanitized before→current diff, secrets
			// masked. Read-only; reverted entries stay previewable.
			r.Get("/audit/{id}/diff", handler.PreviewAudit(auditdiff.New(s, box)))

			// Notification settings (Phase 4).
			r.Get("/settings/notifications", handler.GetNotificationSettings(s, box))
			r.Put("/settings/notifications", handler.PutNotificationSettings(s, box))
			if notifier != nil {
				r.Post("/settings/notifications/test", handler.TestNotification(notifier))
			}

			// DNS provider automation settings (dns-automation plan A). GET always
			// works (returns enabled=false when the flag is off so the UI hides the
			// section); the write grants the box DNS-zone access, so it is step-up
			// gated and 404s when the feature flag is off.
			r.Get("/settings/dns", handler.GetDNSSettings(s, cfg.DNSAutomation))
			r.With(middleware.RequireStepUp).Put("/settings/dns", handler.PutDNSSettings(s, box, cfg.DNSAutomation))

			// Instance-level settings & operations (info, base domain, DNS
			// check, danger-zone control-plane ops).
			r.Route("/instance", func(r chi.Router) {
				// One Checker per process so opening settings repeatedly serves the
				// cached release info instead of re-hitting GitHub each load.
				updateChecker := selfupdate.New(cfg.Version)
				r.Get("/info", handler.InstanceInfo(cfg))
				r.Get("/update-check", handler.UpdateCheck(updateChecker))
				r.Get("/base-domain", handler.GetBaseDomain(s, cfg))
				r.Put("/base-domain", handler.PutBaseDomain(s, cfg, baseDom, routeRec))
				r.Get("/deploy-concurrency", handler.GetDeployConcurrency(s))
				r.Put("/deploy-concurrency", handler.PutDeployConcurrency(s))
				r.Get("/dns-check", handler.DNSCheck(hostIP, dnsResolver))
				// First-run onboarding checklist (plan 04).
				r.Get("/onboarding", handler.GetOnboarding(s))
				r.Post("/onboarding/dismiss", handler.DismissOnboarding(s))
				if docker != nil {
					// Both kill the control plane / every app — a full outage. Gate on
					// fresh 2FA like the other destructive instance ops (/reset below).
					r.With(middleware.RequireStepUp).Post("/restart-control-plane", handler.RestartControlPlane(docker))
					r.With(middleware.RequireStepUp).Post("/stop-all-apps", handler.StopAllApps(s, docker, proxyMgr))
					// Disk maintenance: report docker disk usage and reclaim it on
					// demand (dangling images + unused build cache). Safe — never
					// removes a service's live or rollback image.
					r.Get("/disk", handler.DiskUsage(docker))
					r.Post("/prune", handler.PruneDisk(docker))
					// Fleet-wide storage: per-app volume totals (aggregated from the
					// collector's samples) + the host disk breakdown, one request.
					r.Get("/storage", handler.InstanceStorage(s, docker))
					// Irreversible box-wide wipe — gate on fresh 2FA (on top of
					// the typed "RESET" confirmation the handler enforces).
					r.With(middleware.RequireStepUp).Post("/reset", handler.ResetInstance(s, docker, proxyMgr, cfg.WorkDir))
					// Streams a portable instance bundle (control DB + secrets +
					// master key). Maximally sensitive, so fresh 2FA like the wipes
					// above; the host CLI carries app data volumes on top of this.
					r.With(middleware.RequireStepUp).Post("/migration-bundle", handler.ExportInstanceBundle(docker, cfg))
				}
			})

			// Domains hub (plan 09): add/verify/assign/edit/delete every domain
			// in one place, with the live DNS/cert status engine behind it.
			r.Route("/domains", func(r chi.Router) {
				r.Get("/", handler.ListDomainsHub(s, domStatus, autoList))
				r.Post("/", handler.AddDomainHub(s, syncer, domStatus, dnsAutomator))
				r.Post("/refresh", handler.RefreshDomainStatus(domStatus))
				r.Patch("/{id}", handler.UpdateDomainHub(s, syncer, domStatus))
				r.Delete("/{id}", handler.DeleteDomainHub(s, syncer, dnsAutomator))
				// Bring-your-own TLS cert (dns-automation plan B). Installing the
				// cert a host serves is sensitive — gate on fresh 2FA like the
				// other destructive routes.
				r.With(middleware.RequireStepUp).Post("/{id}/cert", handler.UploadDomainCert(s, box, certSyncer))
				r.With(middleware.RequireStepUp).Delete("/{id}/cert", handler.ClearDomainCert(s, certSyncer))
			})

			r.Route("/apps", func(r chi.Router) {
				r.Get("/", handler.ListApps(s, addonCatalog))
				r.Post("/", handler.CreateApp(s, addonCatalog))
				// Portability (plan 18): import an app from a vac.app.yaml spec, or
				// export one as a spec. /import is a distinct static path; it does
				// not collide with POST "/" or the "/{id}" routes below.
				r.Post("/import", handler.ImportApp(s, box, syncer))
				// Probe a repo for a .env.example so the new-app wizard can pre-fill
				// env vars. Static path; clones without a key (public repos only).
				r.Post("/env-example", handler.EnvExample(nil))
				// Probe a repo for a compose file so the wizard can pre-fill the
				// compose path. Static path; clones without a key (public repos only).
				r.Post("/detect-compose", handler.DetectCompose(nil))
				// Probe a repo for its build source (compose / Dockerfile /
				// framework) so the wizard can pre-select a build kind and badge
				// the detected framework. Static path; keyless (public repos only).
				r.Post("/detect-build", handler.DetectBuild(nil))
				// Global preview budget (count vs VAC_MAX_PREVIEWS). Static path —
				// resolved before "/{id}".
				r.Get("/previews/budget", handler.PreviewBudget(s, previews))
				r.Get("/{id}", handler.GetApp(s, addonCatalog))
				r.Get("/{id}/volumes", handler.GetAppVolumes(s))
				r.Get("/{id}/export", handler.ExportApp(s, box))
				r.Patch("/{id}", handler.UpdateApp(s, addonCatalog))
				// Deleting an app tears down its stack + volumes — gate on fresh 2FA.
				// Pass the worker (when wired) so the delete can interrupt an
				// in-flight deploy and free its pool slot; keep the interface nil
				// in tests where the worker is absent (a nil *Worker boxed in the
				// interface would be non-nil and panic on Cancel).
				var deployInterrupter handler.DeployInterrupter
				if worker != nil {
					deployInterrupter = worker
				}
				r.With(middleware.RequireStepUp).Delete("/{id}", handler.DeleteApp(s, proxyMgr, dbDeprov, docker, deployInterrupter, cfg.WorkDir))

				r.Get("/{id}/ssh-key", handler.GetAppSSHKey(s, keys))
				r.Post("/{id}/ssh-key/regenerate", handler.RegenerateAppSSHKey(s, keys))
				r.Delete("/{id}/ssh-key", handler.DeleteAppSSHKey(keys))

				r.Post("/{id}/test-connection", handler.TestConnection(s, keys, nil))

				if worker != nil {
					r.Post("/{id}/deployments", handler.TriggerDeployment(s, worker))
					r.Get("/{id}/deployments", handler.ListDeployments(s))
					// Approval gate (maintenance-mode-and-deploy-gates.md, Phase 4).
					// `pending` is a static segment, matched ahead of the {did} param.
					r.Get("/{id}/deployments/pending", handler.ListPendingDeployments(s))
					r.Post("/{id}/deployments/{did}/approve", handler.ApproveDeployment(s, worker))
					r.Post("/{id}/deployments/{did}/reject", handler.RejectDeployment(s))
					r.Get("/{id}/deployments/{did}", handler.GetDeployment(s))
					r.Get("/{id}/deployments/{did}/logs", handler.GetDeploymentLogs(s))
					r.Post("/{id}/deployments/{did}/rollback", handler.RollbackDeployment(s, worker))
					r.Post("/{id}/deployments/{did}/cancel", handler.CancelDeployment(s, worker))
				}

				// Preview deployments (preview-deployments.md): list a parent app's
				// previews and tear one down. The list is config-only; teardown needs
				// the lifecycle service (nil → 503 in tests where it isn't wired).
				r.Get("/{id}/previews", handler.ListPreviews(s, autoList))
				r.With(middleware.RequireStepUp).Delete("/{id}/previews/{previewId}", handler.TeardownPreview(s, previews))

				// Push-to-deploy config (plan 01): trigger rules + the inbound
				// webhook URL/secret. These only read/write config, so unlike the
				// inbound endpoint they don't depend on the deploy worker.
				r.Get("/{id}/triggers", handler.ListDeployTriggers(s))
				r.Post("/{id}/triggers", handler.CreateDeployTrigger(s))
				r.Delete("/{id}/triggers/{triggerId}", handler.DeleteDeployTrigger(s))
				// Maintenance mode + editable page (docs/plans/
				// maintenance-mode-and-deploy-gates.md). Toggling re-syncs the proxy,
				// which swaps every host's route to/from the 503 page in place.
				r.Get("/{id}/maintenance", handler.GetMaintenance(s))
				r.Put("/{id}/maintenance", handler.SetMaintenance(s, proxyMgr))
				r.Get("/{id}/maintenance/page", handler.GetMaintenancePage(s))
				r.Put("/{id}/maintenance/page", handler.PutMaintenancePage(s, proxyMgr))
				r.Delete("/{id}/maintenance/page", handler.DeleteMaintenancePage(s, proxyMgr))

				// Deploy windows (maintenance-mode-and-deploy-gates.md, Phase 3):
				// restrict push-to-deploy to time windows; pushes outside park as
				// `scheduled` until the window sweeper releases them.
				r.Get("/{id}/deploy-window", handler.GetDeployWindow(s))
				r.Put("/{id}/deploy-window", handler.PutDeployWindow(s))

				// Scale-to-zero per-app opt-in (docs/plans/scale-to-zero.md). Writes
				// config only; the sweeper (gated on VAC_IDLE_SUSPEND) acts on it.
				r.Get("/{id}/idle-suspend", handler.GetIdleSuspend(s))
				r.Put("/{id}/idle-suspend", handler.SetIdleSuspend(s))

				// Per-app edge rate limit (requests/min/IP). Writing it re-syncs
				// the proxy so Caddy adds/removes the rate_limit handler.
				r.Get("/{id}/rate-limit", handler.GetRateLimit(s))
				r.Put("/{id}/rate-limit", handler.SetRateLimit(s, proxyMgr))

				r.Get("/{id}/webhook", handler.GetAppWebhookConfig(s))
				r.Post("/{id}/webhook/regenerate", handler.RegenerateAppWebhookSecret(s, box))
				r.Delete("/{id}/webhook", handler.DeleteAppWebhookSecret(s))

				r.Get("/{id}/env", handler.ListAppEnv(s, box))
				r.Put("/{id}/env", handler.ReplaceAppEnv(s, box))
				r.Get("/{id}/env/{key}/reveal", handler.RevealAppEnv(s, box))

				// Private-registry credentials for image-sourced apps. Sealed write
				// (never echoed back); the password is write-only like an env secret.
				r.Get("/{id}/registry-auth", handler.GetAppRegistryAuth(s, box))
				r.Put("/{id}/registry-auth", handler.SetAppRegistryAuth(s, box))
				r.Delete("/{id}/registry-auth", handler.DeleteAppRegistryAuth(s))

				r.Get("/{id}/services", handler.ListAppServices(s))
				r.Patch("/{id}/services/{name}", handler.PatchAppService(s, proxyMgr))

				// Domains: per-app view (custom + derived auto hosts, with live
				// DNS/cert status). The Settings → Domains hub uses /api/domains.
				r.Get("/{id}/domains", handler.ListAppDomains(s, domStatus, autoList))
				r.Post("/{id}/services/{name}/domains", handler.AddCustomDomain(s, syncer))
				r.Delete("/{id}/domains/{domainId}", handler.DeleteAppDomain(s, syncer))

				// Request-rate metrics (Phase 3).
				r.Get("/{id}/metrics", handler.AppMetrics(s))
				r.Get("/{id}/services/{name}/metrics", handler.ServiceMetrics(s))

				// Scheduled jobs (plan: scheduled-jobs.md). A core feature — not
				// gated by the managed-services flag (unlike backups/databases). The
				// scheduler goroutine still only starts when ≥1 enabled job exists.
				r.Get("/{id}/jobs", handler.ListJobs(s))
				r.Post("/{id}/jobs", handler.CreateJob(s))
				r.Put("/{id}/jobs/{jid}", handler.UpdateJob(s))
				r.Delete("/{id}/jobs/{jid}", handler.DeleteJob(s))
				r.Get("/{id}/jobs/{jid}/runs", handler.ListJobRuns(s))
				if jobsEngine != nil {
					r.Post("/{id}/jobs/{jid}/run", handler.RunJob(s, jobsEngine))
				}

				// Managed backups (Track D / D1). Gated by VAC_MANAGED_SERVICES so
				// the surface stays closed until the operator opts in; the UI hides
				// the tab on the same flag (instance info → managed_services).
				if cfg.ManagedServices {
					r.Get("/{id}/backups", handler.ListBackups(s, backupRestorer, backupVerifier))
					r.Post("/{id}/backups", handler.CreateBackup(s, box))
					r.Put("/{id}/backups/{cid}", handler.UpdateBackup(s, box))
					// Deleting a backup config discards its run history and artifacts —
					// destructive like restore, so it gets the same fresh-2FA gate.
					r.With(middleware.RequireStepUp).Delete("/{id}/backups/{cid}", handler.DeleteBackup(s))
					r.Get("/{id}/backups/{cid}/runs", handler.ListBackupRuns(s))
					r.Get("/{id}/backups/runs/{rid}/download", handler.DownloadBackup(s, box, cfg.WorkDir))
					if backupEngine != nil {
						r.Post("/{id}/backups/{cid}/run", handler.RunBackup(s, backupEngine))
					}
					// Restore is destructive (overwrites live data, no rollback), so it
					// is fronted by RequireStepUp (fresh 2FA) like delete-app and
					// reset-instance, plus a typed confirmation in the UI.
					if backupRestorer != nil {
						r.Get("/{id}/backups/{cid}/restores", handler.ListBackupRestores(s))
						r.With(middleware.RequireStepUp).
							Post("/{id}/backups/runs/{rid}/restore", handler.RestoreBackup(s, backupRestorer))
					}
					// Backup verification (restorability check) — non-destructive, so
					// no step-up gate. It restores into a throwaway scratch DB.
					if backupVerifier != nil {
						r.Get("/{id}/backups/{cid}/verifications", handler.ListBackupVerifications(s))
						r.Post("/{id}/backups/{cid}/verify", handler.VerifyBackup(s, backupVerifier))
					}
				}

				// Managed databases (Track D / D2). Same gate as backups.
				if cfg.ManagedServices && dbHandler != nil {
					r.Get("/{id}/databases", handler.ListDatabases(s, dbHandler))
					r.Get("/{id}/databases/engines", handler.ListDatabaseEngines(dbHandler))
					r.Post("/{id}/databases", handler.AddDatabase(s, dbHandler))
					// Dropping a managed database destroys its data with no rollback —
					// gate it behind fresh 2FA like delete-app/restore.
					r.With(middleware.RequireStepUp).Delete("/{id}/databases/{dbid}", handler.RemoveDatabase(s, dbHandler))
				}

				if docker != nil {
					r.Post("/{id}/start", handler.StartApp(s, docker, proxyMgr, crashReset))
					r.Post("/{id}/stop", handler.StopApp(s, docker, proxyMgr))
					r.Post("/{id}/restart", handler.RestartApp(s, docker, proxyMgr, crashReset))
					r.Post("/{id}/services/{name}/restart", handler.RestartService(s, docker, proxyMgr, crashReset))
					r.Post("/{id}/services/{name}/stop", handler.StopService(s, docker, proxyMgr))
					r.Post("/{id}/services/{name}/start", handler.StartService(s, docker, proxyMgr, crashReset))
				}

				// Per-app real-time streams (Phase 4). Server-push only.
				if hub != nil {
					r.Get("/{id}/logs", handler.RuntimeLogsWS(s, hub, wsOpts))
					r.Get("/{id}/services/{name}/logs", handler.RuntimeLogsWS(s, hub, wsOpts))
					r.Get("/{id}/stats", handler.StatsWS(s, hub, wsOpts))
				}

				// Interactive container shell (P3.4). Privileged + highest
				// blast-radius: the sandboxed control plane shells into a user app
				// container. Off unless explicitly enabled; each session is
				// audit-logged from the handler (the WS GET escapes the audit
				// middleware, which only wraps mutating verbs).
				//
				// Step-up gated like the other destructive routes: a fresh re-auth is
				// required. A browser can't read the 403 off a failed WS upgrade to
				// trigger the step-up modal, so the UI first hits the REST preflight
				// (which surfaces step_up_required normally) and only opens the socket
				// on 200; the WS itself also carries RequireStepUp as the real gate,
				// honoured because step-up freshness lives on the session row.
				if docker != nil && cfg.EnableShell {
					r.With(middleware.RequireStepUp).Post("/{id}/services/{name}/exec/preflight", handler.ExecPreflight)
					r.With(middleware.RequireStepUp).Get("/{id}/services/{name}/exec", handler.ExecWS(s, docker, wsOpts))
				}
			})

			// Box-wide database inventory (plan 20). Global surface, gated by the
			// same managed-services flag as the per-app database routes.
			if cfg.ManagedServices && dbHandler != nil {
				r.Get("/databases", handler.DatabaseInventory(dbHandler))
			}

			// Box-wide backups overview: read-only fleet view (every config + its
			// last run, a health summary, uncovered services). Edits stay on the
			// per-app surface. Same managed-services gate as the per-app routes.
			if cfg.ManagedServices {
				r.Get("/backups", handler.ListAllBackups(s, cfg.WorkDir))
			}

			// Add-on catalog (Track D / D3). Global surface (installs become
			// apps); gated by the managed-services flag like backups/databases.
			if cfg.ManagedServices && addonCatalog != nil {
				r.Get("/addons", handler.ListAddons(addonCatalog, dbHandler))
				r.Get("/addons/{id}", handler.GetAddon(addonCatalog))
				if addonInstaller2 != nil {
					r.Post("/addons/{id}/install", handler.InstallAddon(addonCatalog, addonInstaller2))
				}
			}

			// Instance-wide deploy queue (plan 20): the current snapshot of
			// running + queued deploys across all apps. /active is the REST
			// snapshot / no-WS fallback; /stream pushes a fresh snapshot on every
			// deploy state change.
			r.Get("/deployments/active", handler.ListActiveDeployments(s))

			// Real-time WebSocket streams (Phase 4). Server-push only; gated by
			// RequireSession above + an Origin check in Accept.
			if hub != nil {
				r.Get("/deployments/{did}/logs", handler.BuildLogsWS(s, hub, wsOpts))
				r.Get("/deployments/stream", handler.DeploymentsWS(s, hub, wsOpts))
			}

			// Host vitals: JSON snapshot, or a live stream on WS upgrade.
			if hub != nil && statsProv != nil {
				r.Get("/host/stats", handler.HostStats(statsProv, hub, wsOpts))
			}
			// Box RAM budget: allocated-vs-total for the dashboard panel, plus a
			// per-app breakdown (committed cap vs live actual usage).
			if statsProv != nil {
				r.Get("/host/budget", handler.HostBudget(statsProv, s))
				r.Get("/host/capacity", handler.HostCapacity(statsProv, s))
			}
			// Box-wide request series (all apps summed), for the dashboard's
			// traffic sparkline. Downsampled server-side to a few dozen points.
			r.Get("/host/metrics", handler.HostMetrics(s))

			// Log Explorer: free-text search across the runtime-log ring buffer
			// (all apps). Historical search is a plain paginated GET; live
			// tailing stays on the per-app WS streams.
			r.Get("/logs/search", handler.SearchRuntimeLogsHandler(s))

			// Security dashboard (plan 15 / E2). Read-only: posture checklist,
			// live traffic snapshot, and capability-detected fail2ban/firewall
			// state. No mutation paths, so the audit middleware logs the GETs and
			// nothing more.
			if secPosture != nil {
				r.Get("/security/posture", handler.SecurityPostureHandler(secPosture))
			}
			r.Get("/security/traffic", handler.SecurityTrafficHandler(secTraffic))
			// Unauthenticated attempts (failed logins, probes) diverted out of the
			// activity feed. No host agent needed — it's the control plane's own
			// request stream — so it's always wired.
			r.Get("/security/attempts", handler.SecurityAttemptsHandler(s))
			if secHost != nil {
				r.Get("/security/fail2ban", handler.SecurityFail2banHandler(secHost))
				r.Get("/security/firewall", handler.SecurityFirewallHandler(secHost))
				// Manual ban override: destructive-ish, so gate on fresh 2FA.
				r.With(middleware.RequireStepUp).Post("/security/fail2ban/ban", handler.SecurityBanHandler(secHost))
			}
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			handler.WriteError(w, http.StatusNotFound, "not found")
		})
	})

	uiHandler, err := ui.Handler()
	if err != nil {
		return nil, fmt.Errorf("server: ui handler: %w", err)
	}
	r.Handle("/*", uiHandler)

	return &http.Server{
		Addr:              cfg.Addr(),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		// Bound the time a client may take to send the full request body, and how
		// long an idle keep-alive connection may sit, as cheap Slowloris hardening.
		// (middleware.Timeout only cancels the request *context* — it doesn't abort
		// a body read that ignores ctx.) No WriteTimeout: WebSocket/log-stream
		// handlers are long-lived and manage their own per-frame deadlines.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	}, nil
}

// scrubCaddyAskToken moves a ?token= secret on the Caddy ask route into the
// X-Caddy-Ask-Token header and removes it from the URL, so the downstream request
// logger (which reads RequestURI at entry) never records the shared secret. A
// no-op for every other path and when no token query param is present.
func scrubCaddyAskToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/caddy/ask" {
			if q := r.URL.Query(); q.Get("token") != "" {
				if r.Header.Get("X-Caddy-Ask-Token") == "" {
					r.Header.Set("X-Caddy-Ask-Token", q.Get("token"))
				}
				q.Del("token")
				r.URL.RawQuery = q.Encode()
				r.RequestURI = r.URL.RequestURI()
			}
		}
		next.ServeHTTP(w, r)
	})
}

// dnsResolverAddr is the public recursive resolver the DNS-check button and the
// status engine share. Default 1.1.1.1:53; VAC_DNS_RESOLVER overrides it (and an
// explicit empty value falls back to the system resolver inside PublicResolver).
func dnsResolverAddr() string {
	if v, ok := os.LookupEnv("VAC_DNS_RESOLVER"); ok {
		return v
	}
	return "1.1.1.1:53"
}

// wsAcceptOptions derives the WebSocket Origin policy from config. The SPA is
// served same-origin as the API, so the request's own host is always allowed.
// We additionally allow the configured base domain (and its subdomains) for
// reverse-proxy setups. In local-exposure mode the Origin check is disabled —
// the documented escape hatch for VPN / tunnel access where the Origin host may
// not match the bind address.
func wsAcceptOptions(cfg config.Config) ws.AcceptOptions {
	opts := ws.AcceptOptions{InsecureSkipVerify: cfg.Exposure == config.ExposureLocal}
	if cfg.BaseDomain != "" {
		opts.OriginPatterns = []string{cfg.BaseDomain, "*." + cfg.BaseDomain}
	}
	return opts
}
