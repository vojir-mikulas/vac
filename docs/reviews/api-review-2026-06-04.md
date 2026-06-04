# API code review — `vac-api`

> Review date: 2026-06-04 · Scope: `api/` (~26K LOC, all non-test Go) · Lenses: hidden bugs, security, RAM/leaks
> Method: five focused reviewers across the package map; critical findings verified against source.

## Summary

The codebase is **well-built**: `crypto/rand` throughout, AEAD nonces correct, bcrypt cost 12,
SHA-256-hashed tokens at rest, uniformly parameterized SQL, rows consistently closed, bounded pool,
per-request `Secure` cookies, tight CSP. The issues below sit on top of a solid base.

**Framing — single-operator box.** `setup.go` refuses a 2nd user; apps have no per-user owner column.
So cross-app "IDOR"-shaped gaps are consistency bugs, not privilege escalation. The real external
attack surface is **the import path, user-controlled webhook/S3 URLs, and user Git repos + compose files.**

Suggested fix order: #1, #2, #3 (host/RCE surface) → #4, #5, #6 → the rest.

---

## 🔴 Critical / High — fix before exposing the box

### 1. Import path bypasses ALL git-URL/branch/slug validation → RCE + path traversal
- **Where:** `appspec/appspec.go:184`, `handler/portability.go`, `admin/portability.go`, `gitcli/gitcli.go:49`, `deploy/pipeline.go:185`
- `appspec.Validate` checks only that `source.url` is non-empty. The HTTP create handler enforces
  `gitURLRe`/`slugRe`/`gitRefRe`; the import path (`POST` import and `vac-api apply`) enforces none.
- `gitcli.Clone` passes the URL as a **positional arg with no `--` separator**, and there is no
  `GIT_ALLOW_PROTOCOL` restriction. An imported spec with `source.url: "ext::sh -c id"` gives
  **remote code execution** via git's `ext::` transport; `metadata.slug: "../../../root/.ssh"`
  escapes the workdir (`filepath.Join(WorkDir, slug, "repo")`).
- **Fix:** run the same URL/branch/slug regexes inside `appspec.Validate`. Defense-in-depth: add
  `-c protocol.ext.allow=never` and a `--` separator in `gitcli`.

### 2. S3 backup endpoint has no SSRF guard at all
- **Where:** `backup/s3.go:62` (plain `http.Client`), `backup/s3.go:34` (`validate()`)
- `Endpoint` is user-controlled and never checked against internal addresses; it flows straight into
  the request URL, and `s3Error` reflects up to 2 KB of the response body back to the caller — a
  working SSRF **read** primitive against `169.254.169.254`, `vac-db`, etc.
- **Fix:** give this client the private-address-blocking `DialContext` (share the helper from
  `notify`, dialing the validated IP — see #4).

### 3. Compose preflight fails open — host-escape guard bypassable
- **Where:** `deploy/pipeline.go:270`, `compose/preflight.go`
- The preflight (docker.sock mount, `privileged`, `network_mode: host`, `cap_add`) is **skipped on any
  parse error**, after which the real file is still built+up. It also never sees `include:`/`extends:`,
  which `docker compose up` does resolve. Either path lets a malicious compose mount the Docker socket
  → host root.
- **Fix:** fail **closed** on preflight parse error, and lint `docker compose config` (the
  resolved/merged output) rather than re-parsing the raw file.

---

## 🟠 Medium

### 4. Notify SSRF guard has a DNS-rebinding (TOCTOU) hole
- **Where:** `notify/dispatcher.go:81-91`
- The guard resolves+checks the IPs, then dials by **hostname** — a second, unchecked resolution.
  Low-TTL rebinding or a multi-A record lets the dialed IP differ from the validated one. (Redirects
  and IPv6 *are* correctly covered.)
- **Fix:** dial the validated literal IP:
  `dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))`. SNI still works from the URL.

### 5. TOTP second factor is replayable (~90s) and not throttled per-account
- **Where:** `auth/totp.go:104`, `middleware/ratelimit.go`
- `Verify` uses `Skew:1` and records nothing — a captured 6-digit code is reusable within the window.
  Rate limiting is **per-IP only**, so a botnet gets a fresh budget per source IP against the single
  admin account, with no per-account ceiling.
- **Fix:** persist `last_totp_step` and reject reuse (atomic UPDATE, like the recovery-code path);
  add a per-account failed-attempt counter/lockout for `/auth/login` and `/auth/totp`.

### 6. No per-deploy timeout → one hung clone/build wedges all deploys
- **Where:** `deploy/worker.go:159`
- The deploy context is derived with `WithCancel`, **no timeout** — despite `gitcli`'s own doc
  demanding one. With default `concurrency=1`, a slow Git remote blocks every deployment. The reaper
  only updates the DB row after 30 min; it never calls `Worker.Cancel`, so the subprocess and slot are
  never freed.
- **Fix:** `context.WithTimeout` per deploy in `process`; have the reaper call `Worker.Cancel(id)`.

### 7. Two unbounded maps in always-on goroutines (200 MB budget)
The only genuine monotonic leaks found — both grow one entry per container/service ever seen, never evicted:
- `stats/manager.go:61` — `uptime map[string]time.Time` never deleted; new container ID every redeploy.
- `crashloop/monitor.go:82-85` — `windows`/`tripped`/`oomNotified` only removed on explicit user "recover."
- **Fix:** prune `uptime` in `collectApp` against the live container-ID set; in crashloop, delete the
  key on a `start`/`destroy` event or when the window trims to empty.

### 8. Audit middleware spawns an unbounded goroutine per mutating request
- **Where:** `middleware/audit.go:66` (`go persist(...)`, no bound)
- Under load against a slow DB, goroutines accumulate up to the 5s timeout.
- **Fix:** bounded worker pool / buffered channel, drop-with-counter when saturated.

---

## 🟡 Low / hardening

- **`docker events` child not reaped** on ctx cancel — `dockercli/engine.go:47` returns before
  `cmd.Wait()`, leaving a zombie + leaked FDs per monitor restart. Use `defer cmd.Wait()`.
- **MariaDB provisioning** is injection-safe *only* because identifiers are generated
  (`dbprovision/mariadb.go:88`) — no escaping at the `sh -c` sink, unlike Postgres
  (`pgx.Identifier.Sanitize`). Add a `mustBeIdent` assertion at the boundary so a future
  "custom DB name" feature doesn't become container RCE.
- **App-scope checks missing** on `GetDeployment` (`deployments.go:231`) and `GetDeploymentLogs`
  (`deployment_logs.go:24`) — read `{did}` without verifying `AppID` matches the URL. Consistency,
  not escalation (single operator).
- **`X-Forwarded-Proto` trusts an unauthenticated header** for the `Secure` flag
  (`handler/cookies.go:21`) when running raw-HTTP. Gate on a config flag instead.
- **Modulo bias** in recovery-code and DB-password generation (`totp.go:184`,
  `dbprovision/engine.go:147`) — negligible entropy loss, but textbook. Rejection-sample.
- **WS per-subscriber buffer** is 256 frames × up-to-1 MiB log frames (`ws/hub.go:12`) — not a leak
  (drop-slow is correct), but a possible transient spike past 200 MB on a noisy `logs:` topic under a
  slow client. Consider bounding frame size.
- **Build-log scanner errors dropped** (`dockercli/compose.go:261`) — a >1 MiB build line silently
  truncates the rest of the log.

---

## Verified clean (no action)

AEAD (no nonce reuse), session/API token generation + revocation, CSRF double-submit, setup token
one-time use, recovery-code atomic consume, SQL injection (none — all `$N` placeholders), rows/conn
leaks (all deferred + `rows.Err()`), pool sizing (25 vs deploy cap 8), logstream/dockerevents/stats
subscription lifecycles, all schedulers' ticker/ctx handling, secret-in-error leakage, Caddy config
injection (JSON not Caddyfile, hostnames normalized).
