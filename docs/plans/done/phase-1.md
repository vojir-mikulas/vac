# Phase 1 — Foundation

## Goal

A running `vac-api` binary that serves the embedded React UI, persists to Postgres, lets a fresh
admin sign up via the setup wizard, log in with password + optional TOTP, and create/list/edit/delete
apps. No deployment pipeline yet — that's Phase 2. At the end of Phase 1, the control plane is a
secure, authenticated app shell with empty app records you can CRUD.

Reference: see `mvp.md` § Build Phases → Phase 1 for the original scope. This document sequences
that scope, picks libraries, and defines exit criteria.

---

## Scope

### In

- HTTP server on `chi` with middleware stack (recovery, request ID, logger, CSRF, auth, rate limit)
- `go:embed` of the React build into the Go binary, single-binary deploy
- Config loader (defaults → `vac.yaml` → env vars)
- Postgres connection pool + embedded migrations
- Encryption helpers (AES-256-GCM) keyed by `VAC_MASTER_KEY`
- First-boot detection + console onboarding banner
- Setup-wizard backend endpoints (admin account creation, instance settings)
- Authentication: bcrypt password, session cookies, `/me`, logout
- CSRF (double-submit cookie pattern)
- TOTP 2FA: setup, verify, disable, recovery codes
- Rate limiting on `/api/auth/login`
- Session management: list active sessions, revoke one, revoke all others
- API tokens: create, list, revoke
- App CRUD: `GET / POST / GET :id / PATCH / DELETE` on `/api/apps`
- `/health` endpoint (no auth, no rate limit)
- Unit tests on the security-critical paths (auth, crypto, CSRF, rate limit)
- Integration test that spins up Postgres in a container and walks the setup-wizard → login → app create flow

### Out (deferred to later phases)

- Git clone / SSH key generation (Phase 2)
- Compose detection, docker build/up (Phase 2)
- Caddy integration, custom domains, TLS (Phase 3)
- WebSocket hub, live logs, stats streaming (Phase 4)
- Dashboard UI beyond a single placeholder route (Phase 5)
- Notifications (Phase 4)

---

## Library decisions

| Concern | Pick | Why |
|---|---|---|
| HTTP router | `github.com/go-chi/chi/v5` | Groups, sub-routers, per-route middleware. Tiny, zero deps. |
| Postgres driver | `github.com/jackc/pgx/v5` + `pgxpool` | Native protocol, fast, modern. No `database/sql` indirection unless we need it. |
| Migrations | `github.com/pressly/goose/v3` | Embedded migrations runnable from Go on startup — fits the single-binary model. |
| Password hashing | `golang.org/x/crypto/bcrypt` | Stdlib-adjacent, battle-tested. Cost 12. |
| TOTP | `github.com/pquerna/otp` | The standard Go TOTP library. RFC 6238 + QR helpers. |
| Symmetric crypto | `crypto/aes` + `crypto/cipher` (stdlib) | AES-256-GCM is in the stdlib — no third-party needed. |
| Rate limiting | `golang.org/x/time/rate` | Per-IP `*rate.Limiter` map with TTL eviction. Standard pattern, no external dep. |
| Validation | `github.com/go-playground/validator/v10` | Tag-based input validation on request structs. |
| UUID | `github.com/google/uuid` | App IDs, session IDs, token IDs. |
| Test containers | `github.com/testcontainers/testcontainers-go` | Real Postgres for integration tests. |
| Mock-free HTTP testing | `net/http/httptest` (stdlib) | Sufficient for handler tests. |

**Not adopting yet:**
- `sqlc` — codegen is nice but adds a build step. Re-evaluate once the query surface grows past ~20.
- `gorilla/websocket` / `coder/websocket` — Phase 4 concern, not now.
- Any logging library — `log/slog` (stdlib) covers MVP needs.

---

## File layout (end of Phase 1)

```
api/
├── main.go                            # bootstrap, signal handling, calls server.Run
├── go.mod / go.sum
├── .golangci.yml
├── Dockerfile
├── migrations/
│   ├── 00001_users_sessions.sql
│   ├── 00002_apps.sql
│   ├── 00003_api_tokens.sql
│   └── ...
└── internal/
    ├── config/
    │   ├── config.go                  # struct + Load() with precedence
    │   └── config_test.go
    ├── db/
    │   ├── db.go                      # pgxpool + goose runner
    │   ├── migrate.go                 # //go:embed migrations/*.sql
    │   └── db_test.go
    ├── crypto/
    │   ├── aead.go                    # AES-256-GCM seal/open with VAC_MASTER_KEY
    │   └── aead_test.go
    ├── server/
    │   ├── server.go                  # chi router wiring + graceful shutdown
    │   ├── middleware/
    │   │   ├── recovery.go
    │   │   ├── logger.go
    │   │   ├── auth.go                # session cookie → context.User
    │   │   ├── csrf.go
    │   │   └── ratelimit.go
    │   └── handler/
    │       ├── health.go
    │       ├── auth.go                # login, logout, /me, sessions, api-tokens
    │       ├── totp.go
    │       ├── setup.go               # first-boot admin creation
    │       └── apps.go                # App CRUD
    ├── auth/
    │   ├── password.go                # bcrypt wrappers
    │   ├── session.go                 # create/lookup/revoke session
    │   ├── totp.go                    # setup, verify, recovery codes
    │   ├── token.go                   # API token issuance + hash
    │   └── auth_test.go
    ├── store/
    │   ├── users.go                   # all users/sessions/totp/tokens queries
    │   └── apps.go                    # apps queries
    └── ui/
        ├── embed.go                   # //go:embed dist + handler
        └── dist/                      # populated by vite build, gitignored
```

`internal/store/` is intentionally thin (Postgres-flavoured CRUD) — no domain logic. Handlers
call store directly until something repeats; refactor to a service layer only when it does.

---

## Sequence

### M1 — HTTP foundation (chi + go:embed + config)

**Goal:** binary boots, serves UI from embed, has middleware skeleton wired up.

- Add chi: `go get github.com/go-chi/chi/v5`
- Rewrite `internal/server/server.go` to build a `chi.Mux`:
  - `r.Use(middleware.RequestID, middleware.Recoverer, middleware.Logger)` (chi built-ins)
  - Mount `/health` (public)
  - Mount `/api/*` group (will gain auth middleware in M5)
  - Mount static UI: catch-all that falls back to `index.html` for SPA routes
- Add `internal/ui/embed.go` with `//go:embed all:dist` — guard with a build tag fallback so a fresh
  clone with no `dist/` still compiles (empty FS in dev, real FS in release):
  - `//go:build embedui` for the real embed, default stub serves a "UI not built" page
  - Update `make build` to pass `-tags embedui` after `vite build` runs
- Add `internal/config/config.go`:
  - Struct mirroring the env vars from `mvp.md` § Configuration
  - `Load()` reads defaults → optional `VAC_CONFIG_FILE` yaml → env vars
  - Validates `VAC_MASTER_KEY` is 32 bytes hex (warn-only at this stage; M3 enforces it for app creation)
- Wire `main.go` to `config.Load()` → `server.New(cfg)`

**Test:** `curl :3000/health` returns `{"status":"ok"}`; `curl :3000/` returns the UI.

### M2 — Postgres + migrations

**Goal:** binary connects to Postgres, runs migrations on startup, has a pool ready for handlers.

- Top-level `compose.yaml` (repo root) for dev:
  - `vac-db` (postgres:16, named volume, exposed on 5432 for psql convenience)
  - `vac-api` (built from `api/Dockerfile`, depends_on db)
  - `.env.example` with `VAC_DATABASE_URL`, `VAC_MASTER_KEY`
- Add `internal/db/db.go`: `pgxpool.New(ctx, cfg.DatabaseURL)` with sensible defaults (max 25 conns)
- Add `internal/db/migrate.go`: `//go:embed migrations/*.sql`, `goose.Up(pool, "migrations")` on boot
- Write the first migration `00001_users_sessions.sql`:
  - `users` (id, username UNIQUE, password_hash, totp_secret nullable, totp_enabled, totp_recovery_codes JSONB nullable, timestamps)
  - `sessions` (id, user_id FK, token_hash, ip, ua, created_at, expires_at, last_seen_at)
- `internal/store/users.go` with `CreateUser`, `GetUserByUsername`, `CreateSession`, `GetSessionByTokenHash`, `RevokeSession`, `ListSessionsForUser`, `UpdateLastSeen`

**Test:** integration test using testcontainers — spin up Postgres, run migrations, insert + query a user.

### M3 — Crypto + secret helpers

**Goal:** encrypt-at-rest helpers ready for env vars, SSH keys, TOTP secrets.

- `internal/crypto/aead.go`:
  - `Seal(plaintext []byte) (ciphertext []byte, err error)` — random 12-byte nonce prepended
  - `Open(ciphertext []byte) (plaintext []byte, err error)`
  - Key sourced from `cfg.MasterKey` (already hex-decoded by config layer)
- `aead_test.go` covers: round-trip, tampered ciphertext rejected, wrong key rejected, empty plaintext, large plaintext

**Test:** unit tests only — pure function, no I/O.

### M4 — First boot + setup wizard backend

**Goal:** on a fresh database, the API prints the console banner and exposes a single public endpoint
to create the admin account.

- On startup, `store.Users.Count()` — if 0, log the banner to stdout (the boxed text from mvp.md § Onboarding)
- Add a public endpoint `POST /api/setup/admin`:
  - Body: `{username, password}`
  - Refuses if any user already exists (idempotency / replay protection)
  - Creates user, returns 201, **does not auto-login** (per mvp.md spec)
- Add a public probe `GET /api/setup/status` → `{needs_setup: bool}` so the UI knows whether to show
  the wizard or the login page

**Test:** integration test — fresh DB, POST setup/admin succeeds, second POST returns 409.

### M5 — Password auth + sessions + CSRF

**Goal:** users can log in, get a session cookie, hit authenticated endpoints, log out.

- `internal/auth/password.go`: `Hash`, `Verify` wrapping bcrypt with cost 12
- `internal/auth/session.go`:
  - `Create(userID, ip, ua, extended bool)` — generates 32-byte random token, stores SHA-256 hash,
    returns raw token for the cookie
  - `Lookup(rawToken)` — hashes, queries, updates `last_seen_at`, returns user
  - `Revoke(sessionID)`
- Cookie: `vac_session`, HttpOnly, SameSite=Strict, Secure if `VAC_EXPOSURE=public`, Max-Age from TTL config
- `internal/server/middleware/auth.go`:
  - Reads cookie, calls `session.Lookup`, injects `*User` into `r.Context()`
  - Helpers: `RequireAuth` (returns 401 if no user), `User(ctx)` getter
- `internal/server/middleware/csrf.go`:
  - Double-submit pattern: on login, set non-HttpOnly `vac_csrf` cookie (random 32 bytes)
  - On mutating requests (POST/PUT/PATCH/DELETE), require matching `X-CSRF-Token` header
  - Skip for `GET/HEAD/OPTIONS` and Bearer-token requests (API tokens, M9)
- Handlers:
  - `POST /api/auth/login` — verify password, issue session + CSRF cookies
  - `POST /api/auth/logout` — revoke session, clear cookies
  - `GET /api/auth/me` — return current user (auth required)

**Test:**
- Unit: password hash/verify, session token round-trip, CSRF accept/reject
- Integration: setup admin → login → /me works → logout → /me 401

### M6 — TOTP 2FA

**Goal:** users can enable 2FA, login flow detects 2FA-enabled users and requires a code.

- `internal/auth/totp.go`:
  - `Setup(userID)` — generate secret, encrypt with crypto.Seal, store pending (not yet enabled);
    return QR URI + secret
  - `Verify(userID, code)` — uses `otp/totp.Validate` with ±1 window tolerance
  - `Enable(userID, code)` — verify + flip `totp_enabled = true` + generate 10 recovery codes (hashed)
  - `Disable(userID, password)` — re-verify password before disabling
- Login flow:
  - `POST /api/auth/login` — if user has `totp_enabled`, return 200 with pre-auth cookie (short TTL,
    different name e.g. `vac_pre`) and `{"totp_required": true}`
  - `POST /api/auth/totp` — verifies code against pre-auth cookie, upgrades to full session
- Handlers: `POST /api/auth/totp/setup`, `POST /api/auth/totp/verify`, `DELETE /api/auth/totp`

**Test:** unit on `Verify` with known vectors; integration on full login-with-2FA flow.

### M7 — Rate limiting

**Goal:** brute-force login is throttled per IP.

- `internal/server/middleware/ratelimit.go`:
  - Per-key `*rate.Limiter` map (`map[string]*entry` with `lastSeen` for eviction)
  - Background goroutine evicts entries older than 1h
  - Configurable: `VAC_LOGIN_RATE_LIMIT` (5), `VAC_LOGIN_RATE_WINDOW` (15m)
  - Returns 429 with `Retry-After` header when over budget
- Apply to `POST /api/auth/login`, `POST /api/auth/totp`, `POST /api/setup/admin`
- Log failed attempts with IP + UA for the audit trail

**Test:** unit test fires 6 requests, expects the 6th to 429.

### M8 — Session management endpoints

**Goal:** users can see and revoke their active sessions.

- `GET /api/auth/sessions` — list user's sessions (id, ip, ua, created_at, last_seen_at, is_current)
- `DELETE /api/auth/sessions/:id` — revoke one (refuse if it's the current session — use logout for that)
- `DELETE /api/auth/sessions` — revoke all *other* sessions

**Test:** integration — login twice from different "IPs", list shows 2, revoke other, list shows 1.

### M9 — API tokens

**Goal:** programmatic auth path separate from cookies.

- Migration `00003_api_tokens.sql`: `api_tokens (id, user_id, name, token_hash, last_used_at, created_at, expires_at)`
- `internal/auth/token.go`: `Create(userID, name, expiresAt)` returns raw token once; subsequent
  lookups go by SHA-256 hash
- Token format: `vac_<base64url(32 random bytes)>` — easy to grep, easy to revoke leaked tokens
- Middleware: if request has `Authorization: Bearer vac_...`, resolve via token store, bypass CSRF
- Handlers: `GET/POST/DELETE /api/auth/api-tokens`

**Test:** create token, use as Bearer, revoke, fail.

### M10 — App CRUD

**Goal:** first business object end-to-end, proves the whole stack.

- Migration `00004_apps.sql`: per `mvp.md` § Data Model (id uuid, name, slug, git_url, git_branch,
  git_ssh_key_id nullable, compose_file default 'compose.yaml', status enum, timestamps)
- `internal/store/apps.go`: List/Get/Create/Update/Delete
- Slug generation: lowercase + `-` separated; uniqueness enforced via UNIQUE index, return 409 on collision
- Handlers under `/api/apps`, mounted under the auth-required group
- Validation: name required, git URL format check (regex: SSH or HTTPS), branch defaults to `main`

**Test:** integration — login, create app, list shows it, patch name, delete, list empty.

### M11 — Hardening pass

- `/health` returns DB-pinged status (not just "ok") — distinguishes "binary up" from "DB up"
- Graceful shutdown drains active requests, closes pool — already in `main.go`, verify with a test
  that sends a slow request during shutdown
- 401/403/404/500 error responses use a consistent JSON shape `{"error": "...", "code": "..."}`
- All input via `validator` tags; reject early with 400
- All passwords/tokens/secrets never logged — review logger calls
- Run `golangci-lint run` on `api/` and fix all findings
- Verify control plane RSS < 200 MB idle (single goroutine count check)

---

## Testing strategy

| Layer | Tool | What it covers |
|---|---|---|
| Unit | `go test`, stdlib `httptest` | Pure functions: crypto, password, TOTP verify, slug generation, validator rules |
| Handler | `httptest.NewRecorder` + chi router | Per-endpoint: status codes, response shapes, middleware behaviour |
| Integration | `testcontainers-go` Postgres | The flows that must work: setup → login → CRUD; runs in CI |
| Manual | curl + the placeholder UI | Smoke test the happy path before merging each milestone |

**Mocking policy:** do not mock Postgres. The integration tests use a real container — slower but
catches the bugs that matter (schema mismatches, transaction semantics, NULL handling). For
non-DB units (crypto, password hashing), no mocks needed.

**CI (out of scope for Phase 1 setup but reserve a slot):** add GitHub Actions running `make lint`,
`make test`, `make build`. Defer to end of Phase 1 once the integration test exists.

---

## Exit criteria

Phase 1 is done when all of these pass on a fresh clone:

- [ ] `make build` produces a `vac-api` binary that embeds the UI
- [ ] `docker compose up` brings up `vac-db` + `vac-api`, migrations run, `/health` returns DB OK
- [ ] First-boot banner prints on stdout when DB is empty
- [ ] `GET /api/setup/status` returns `{"needs_setup": true}` on a fresh DB
- [ ] `POST /api/setup/admin` creates the first user, second call returns 409
- [ ] `POST /api/auth/login` with correct credentials returns a session cookie
- [ ] Wrong password 401s; brute-force is throttled to 5/15min per IP
- [ ] `GET /api/auth/me` works with the cookie, 401s without
- [ ] CSRF token is required on POST/PUT/PATCH/DELETE for cookie-authed requests
- [ ] TOTP setup + login-with-TOTP flow works end-to-end
- [ ] User can list sessions, revoke a non-current session, revoke-all-others
- [ ] API token can be created, used as Bearer (no CSRF needed), revoked
- [ ] App CRUD works: create returns 201 with the new app, list shows it, patch updates, delete removes
- [ ] `golangci-lint run` is clean
- [ ] Integration test suite passes locally
- [ ] Control plane idles under 200 MB RAM (excluding Postgres)
