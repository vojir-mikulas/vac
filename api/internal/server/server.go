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
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/domainstatus"
	"github.com/vojir-mikulas/vac/api/internal/proxy"
	"github.com/vojir-mikulas/vac/api/internal/revert"
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
func New(ctx context.Context, cfg config.Config, s *store.Store, worker *deploy.Worker, docker *dockercli.Compose, pm *proxy.Manager, hub *ws.Hub, statsProv handler.StatsProvider, notifier handler.TestSender, backupEngine handler.BackupRunner, dbProv *dbprovision.Provisioner, addonCat *addon.Registry, addonInstaller *addon.Installer, dstatus *domainstatus.Engine, secPosture handler.SecurityPosture, secTraffic handler.SecurityTraffic, secHost handler.SecurityHost) (*http.Server, error) {
	// Gate X-Forwarded-Proto trust (cookie Secure decision) on config — the
	// bundled vac-proxy sets the header; a raw-HTTP box can disable trusting it.
	handler.SetTrustForwardedProto(cfg.TrustProxyHeaders)

	// Convert the concrete (possibly-nil) manager into nil-able interface
	// values so handlers' `== nil` guards behave (a typed-nil pointer in an
	// interface is not nil).
	var (
		proxyMgr handler.ProxyManager
		syncer   handler.RouteSyncer
		caddyPin handler.CaddyPinger
		ctrlChk  handler.ControlDomainChecker
		autoChk  handler.AutoHostChecker
		autoList handler.AutoHostLister
		baseDom  handler.BaseDomainSetter
	)
	if pm != nil {
		proxyMgr = pm
		syncer = pm
		caddyPin = pm
		ctrlChk = pm
		autoChk = pm
		autoList = pm
		baseDom = pm
	}
	var domStatus handler.DomainStatusProvider
	if dstatus != nil {
		domStatus = dstatus
	}
	var routeRec handler.RouteReconciler
	if pm != nil {
		routeRec = pm
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

	// One shared limiter across the auth surface: an attacker who burns the
	// password budget should not then get a fresh budget on /auth/totp.
	authLimiter := middleware.NewRateLimiter(ctx, cfg.LoginRateLimit, cfg.LoginRateWindow)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Logger)
	r.Use(chimw.Timeout(60 * time.Second))
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
			Post("/webhooks/{appID}", handler.Webhook(s, box, worker))
	}

	// On-demand-TLS ask hook for Caddy. Unauthenticated by design (Caddy can't
	// present a session); reachable only on the internal compose network.
	r.Get("/internal/caddy/ask", handler.CaddyAsk(s, cfg.CaddyAskToken, ctrlChk, autoChk))

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

			// Instance-level settings & operations (info, base domain, DNS
			// check, danger-zone control-plane ops).
			r.Route("/instance", func(r chi.Router) {
				r.Get("/info", handler.InstanceInfo(cfg))
				r.Get("/base-domain", handler.GetBaseDomain(s, cfg))
				r.Put("/base-domain", handler.PutBaseDomain(s, cfg, baseDom, routeRec))
				r.Get("/deploy-concurrency", handler.GetDeployConcurrency(s))
				r.Put("/deploy-concurrency", handler.PutDeployConcurrency(s))
				r.Get("/dns-check", handler.DNSCheck(hostIP, dnsResolver))
				// First-run onboarding checklist (plan 04).
				r.Get("/onboarding", handler.GetOnboarding(s))
				r.Post("/onboarding/dismiss", handler.DismissOnboarding(s))
				if docker != nil {
					r.Post("/restart-control-plane", handler.RestartControlPlane(docker))
					r.Post("/stop-all-apps", handler.StopAllApps(s, docker, proxyMgr))
					// Irreversible box-wide wipe — gate on fresh 2FA (on top of
					// the typed "RESET" confirmation the handler enforces).
					r.With(middleware.RequireStepUp).Post("/reset", handler.ResetInstance(s, docker, proxyMgr))
				}
			})

			// Domains hub (plan 09): add/verify/assign/edit/delete every domain
			// in one place, with the live DNS/cert status engine behind it.
			r.Route("/domains", func(r chi.Router) {
				r.Get("/", handler.ListDomainsHub(s, domStatus, autoList))
				r.Post("/", handler.AddDomainHub(s, syncer, domStatus))
				r.Post("/refresh", handler.RefreshDomainStatus(domStatus))
				r.Patch("/{id}", handler.UpdateDomainHub(s, syncer, domStatus))
				r.Delete("/{id}", handler.DeleteDomainHub(s, syncer))
			})

			r.Route("/apps", func(r chi.Router) {
				r.Get("/", handler.ListApps(s, addonCatalog))
				r.Post("/", handler.CreateApp(s, addonCatalog))
				// Portability (plan 18): import an app from a vac.app.yaml spec, or
				// export one as a spec. /import is a distinct static path; it does
				// not collide with POST "/" or the "/{id}" routes below.
				r.Post("/import", handler.ImportApp(s, box, syncer))
				r.Get("/{id}", handler.GetApp(s, addonCatalog))
				r.Get("/{id}/export", handler.ExportApp(s, box))
				r.Patch("/{id}", handler.UpdateApp(s, addonCatalog))
				// Deleting an app tears down its stack + volumes — gate on fresh 2FA.
				r.With(middleware.RequireStepUp).Delete("/{id}", handler.DeleteApp(s, proxyMgr, dbDeprov, docker))

				r.Get("/{id}/ssh-key", handler.GetAppSSHKey(s, keys))
				r.Post("/{id}/ssh-key/regenerate", handler.RegenerateAppSSHKey(s, keys))
				r.Delete("/{id}/ssh-key", handler.DeleteAppSSHKey(keys))

				r.Post("/{id}/test-connection", handler.TestConnection(s, keys, nil))

				if worker != nil {
					r.Post("/{id}/deployments", handler.TriggerDeployment(s, worker))
					r.Get("/{id}/deployments", handler.ListDeployments(s))
					r.Get("/{id}/deployments/{did}", handler.GetDeployment(s))
					r.Get("/{id}/deployments/{did}/logs", handler.GetDeploymentLogs(s))
					r.Post("/{id}/deployments/{did}/rollback", handler.RollbackDeployment(s, worker))
					r.Post("/{id}/deployments/{did}/cancel", handler.CancelDeployment(s, worker))
				}

				// Push-to-deploy config (plan 01): trigger rules + the inbound
				// webhook URL/secret. These only read/write config, so unlike the
				// inbound endpoint they don't depend on the deploy worker.
				r.Get("/{id}/triggers", handler.ListDeployTriggers(s))
				r.Post("/{id}/triggers", handler.CreateDeployTrigger(s))
				r.Delete("/{id}/triggers/{triggerId}", handler.DeleteDeployTrigger(s))
				r.Get("/{id}/webhook", handler.GetAppWebhookConfig(s))
				r.Post("/{id}/webhook/regenerate", handler.RegenerateAppWebhookSecret(s, box))
				r.Delete("/{id}/webhook", handler.DeleteAppWebhookSecret(s))

				r.Get("/{id}/env", handler.ListAppEnv(s, box))
				r.Put("/{id}/env", handler.ReplaceAppEnv(s, box))
				r.Get("/{id}/env/{key}/reveal", handler.RevealAppEnv(s, box))

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

				// Managed backups (Track D / D1). Gated by VAC_MANAGED_SERVICES so
				// the surface stays closed until the operator opts in; the UI hides
				// the tab on the same flag (instance info → managed_services).
				if cfg.ManagedServices {
					r.Get("/{id}/backups", handler.ListBackups(s))
					r.Post("/{id}/backups", handler.CreateBackup(s, box))
					r.Put("/{id}/backups/{cid}", handler.UpdateBackup(s, box))
					r.Delete("/{id}/backups/{cid}", handler.DeleteBackup(s))
					r.Get("/{id}/backups/{cid}/runs", handler.ListBackupRuns(s))
					r.Get("/{id}/backups/runs/{rid}/download", handler.DownloadBackup(s, box, cfg.WorkDir))
					if backupEngine != nil {
						r.Post("/{id}/backups/{cid}/run", handler.RunBackup(s, backupEngine))
					}
				}

				// Managed databases (Track D / D2). Same gate as backups.
				if cfg.ManagedServices && dbHandler != nil {
					r.Get("/{id}/databases", handler.ListDatabases(s, dbHandler))
					r.Get("/{id}/databases/engines", handler.ListDatabaseEngines(dbHandler))
					r.Post("/{id}/databases", handler.AddDatabase(s, dbHandler))
					r.Delete("/{id}/databases/{dbid}", handler.RemoveDatabase(s, dbHandler))
				}

				if docker != nil {
					r.Post("/{id}/start", handler.StartApp(s, docker, proxyMgr))
					r.Post("/{id}/stop", handler.StopApp(s, docker, proxyMgr))
					r.Post("/{id}/restart", handler.RestartApp(s, docker, proxyMgr))
					r.Post("/{id}/services/{name}/restart", handler.RestartService(s, docker, proxyMgr))
					r.Post("/{id}/services/{name}/stop", handler.StopService(s, docker, proxyMgr))
					r.Post("/{id}/services/{name}/start", handler.StartService(s, docker, proxyMgr))
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
				if docker != nil && cfg.EnableShell {
					r.Get("/{id}/services/{name}/exec", handler.ExecWS(s, docker, wsOpts))
				}
			})

			// Box-wide database inventory (plan 20). Global surface, gated by the
			// same managed-services flag as the per-app database routes.
			if cfg.ManagedServices && dbHandler != nil {
				r.Get("/databases", handler.DatabaseInventory(dbHandler))
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
			// Box RAM budget: allocated-vs-total for the dashboard panel.
			if statsProv != nil {
				r.Get("/host/budget", handler.HostBudget(statsProv, s))
			}

			// Security dashboard (plan 15 / E2). Read-only: posture checklist,
			// live traffic snapshot, and capability-detected fail2ban/firewall
			// state. No mutation paths, so the audit middleware logs the GETs and
			// nothing more.
			if secPosture != nil {
				r.Get("/security/posture", handler.SecurityPostureHandler(secPosture))
			}
			r.Get("/security/traffic", handler.SecurityTrafficHandler(secTraffic))
			if secHost != nil {
				r.Get("/security/fail2ban", handler.SecurityFail2banHandler(secHost))
				r.Get("/security/firewall", handler.SecurityFirewallHandler(secHost))
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
	}, nil
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
