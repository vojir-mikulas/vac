# Email (SMTP) Notifications — design sketch

Add **email** as a fourth notification channel alongside Discord / Slack / generic webhook, so
an operator who lives in their inbox (and runs no chat) still gets deploy-failed, crash-loop,
cert-expiring, and friends. One SMTP relay, configured in Settings, env-overridable, with the
password sealed at rest exactly like the webhook URLs and a "send test email" affordance.

Status: **planned** (not started).

The hard work is already done: the `Dispatcher` resolves config, gates on the per-event toggle
map, and fans out to renderers; a test endpoint exists; secrets are already sealed-and-redacted.
Email is "one more channel" — a renderer that speaks SMTP instead of POSTing JSON, plus a wider
settings row.

## Why

The notify subsystem is webhook-only (`notify/dispatcher.go:151` POSTs to Discord/Slack URLs).
Plenty of single-box operators don't run Discord or Slack — email is the lowest-common-denominator
alert channel, and a self-hosted PaaS that can't email you when a deploy fails is missing the most
basic ops signal. Everything around it (event model, toggles, redact-on-read secrets, test ping)
generalises to email without rework.

## What already exists (don't rebuild)

- **Render-neutral `Event` + per-channel renderers.** `Event` (`notify/events.go:31`) carries
  Title/Message/AppName/Service/Commit/Duration/OK; `discordPayload` (`discord.go:34`) and
  `slackPayload` (`slack.go:22`) turn it into channel-specific shapes. An email renderer is the
  same shape → an RFC 5322 message.
- **Fan-out + toggle gate.** `Dispatcher.dispatch` (`dispatcher.go:143`) resolves config, checks
  `cfg.enabled(ev.Type)` (`dispatcher.go:99`, absent toggle = on), then sends to each configured
  channel on a detached goroutine. Email slots in next to the two `d.post(...)` calls
  (`dispatcher.go:151-156`) — same `enabled` gate, no new toggle plumbing.
- **Encrypted-at-rest + redact-on-read, end to end (D8).** Webhook URLs are sealed with
  `crypto.Box` (`crypto/aead.go:43`), stored as `BYTEA` (`migrations/00015`), decrypted only in
  the dispatcher (`dispatcher.go:129` `open`), and the handler returns a "configured + last4"
  hint, never the secret (`handler/notifications.go:113` `sealURLUpdate`, `:143` `last4`). The
  SMTP **password** reuses this verbatim.
- **Env override + master-key gate.** `cfg.NotifyDiscordURL/NotifySlackURL` come from
  `VAC_NOTIFY_*` (`config.go:476`) and win over stored values in `resolve` (`dispatcher.go:111`);
  storing a secret with no `VAC_MASTER_KEY` returns 503 (`handler/notifications.go:120`). SMTP
  mirrors both.
- **Test endpoint.** `Dispatcher.SendTest` (`dispatcher.go:204`) builds a synthetic `Event` and
  sends to every configured channel, returning a count; `POST /settings/notifications/test`
  (`server.go:225`) and `notificationsApi.test()` wire it. Email just becomes one more channel the
  same call reaches.
- **Settings form pattern.** `notifications-section.tsx` renders per-channel "configured?
  +placeholder hint" fields (`ChannelField`, line 124) and the toggle list; redacted-secret editing
  is the `configuredPlaceholder` pattern (line 147). The SMTP block is a sibling form group.

## Key technical realities (read before building)

- **Use `net/smtp` (stdlib) — zero new dep.** It matches VAC's documented "hand-roll to dodge
  deps" house style (cf. the hand-rolled SigV4 S3 signer, deviations Track D / D1). `net/smtp`
  covers `STARTTLS` (`Client.StartTLS`), `PlainAuth`/`CRAMMD5Auth`, and the full envelope. The one
  gap: it has **no implicit-TLS (port 465 / SMTPS) helper** — you `tls.Dial` first, then
  `smtp.NewClient` on that conn. That's ~5 lines, not a dependency. Recommendation: **stdlib**,
  with a small `tlsMode` switch (`starttls` | `implicit` | `none`) deciding whether to wrap the
  conn before/after the greeting.
- **The SSRF netguard does NOT apply to raw SMTP — be honest about it.** `netguard.DialContext`
  (`netguard/netguard.go:56`) is an `http.Transport` dial hook; the dispatcher installs it on its
  `http.Client` (`dispatcher.go:75`). SMTP doesn't go through that client at all. But `netguard`'s
  *core predicate* — `netguard.IsPrivate(ip)` (`netguard.go:36`) — is transport-agnostic. The fit
  for SMTP: resolve the host ourselves, run each IP through `IsPrivate`, reject if any is
  private/loopback/link-local/CGNAT, then dial the validated literal IP (same TOCTOU-safe shape as
  the HTTP guard). **Caveat worth stating:** unlike a webhook, a *legitimate* relay can live on the
  LAN (a sidecar Postfix, `vac-edge`-adjacent MTA). So make the SMTP private-address block an
  **opt-out** (`VAC_NOTIFY_SMTP_ALLOW_PRIVATE`, default off → guarded), not the hard wall the
  webhook guard is. This is the honest difference: webhook URLs should never reach private space;
  an SMTP relay legitimately might.
- **The recipient list is config, not a secret.** `from` and `to` are operator-set addresses, not
  bearer tokens — store them as plaintext columns. Only the **password** is sealed.
- **Send synchronously inside the existing goroutine.** `dispatch` already runs on a detached
  goroutine with a 30s context (`dispatcher.go:144`); a blocking SMTP `SendMail` there is fine and
  needs no new concurrency. Failures get logged, never fatal (same posture as `post`).

## Scope decisions (the important part)

1. **One relay, one recipient set.** Single `from`, a comma/newline-split `to` list. No per-event
   routing, no multiple relays — single operator, single box.
2. **Plain-text body, minimal HTML optional.** Render the `Event` as a short text body
   (Title as Subject, Message + Commit/Service/Duration/deep-link lines). A tiny `text/html`
   alternative is a nice-to-have; ship text first. No templating engine.
3. **Password sealed; everything else plaintext.** Mirror D8 for the password column only.
4. **Private-address guard is opt-out for SMTP** (realities above) — the one deliberate divergence
   from the webhook guard, documented as a new deviation **D10**.
5. **Reuse the existing per-event toggle map unchanged.** Email respects the same `events` JSONB;
   no per-channel event matrix. A muted event is muted on every channel — matches today's model.
6. **`auth` optional.** Some relays (a localhost Postfix) take no auth; empty username → skip
   `PlainAuth`, send unauthenticated.

## Phase 1 — Backend: schema + SMTP renderer + dispatch

- **Migration `000NN_notification_smtp.sql`** — add to `notification_settings` (still the singleton
  row): `smtp_host TEXT`, `smtp_port INT`, `smtp_username TEXT`, `smtp_password_enc BYTEA` (sealed),
  `smtp_from TEXT`, `smtp_to TEXT` (the recipient list), `smtp_tls_mode TEXT` (`starttls`
  default). All nullable; an empty `smtp_host` means "email channel off."
- **Store** — extend `NotificationSettingsRow` (`store/notification_settings.go:13`) with the new
  fields; `GetNotificationSettings`/`PutNotificationSettings` select/upsert them. `_enc` stays
  ciphertext-only, like `DiscordURLEnc`.
- **`notify/email.go`** — `emailMessage(ev, baseURL) (subject, body string)` rendering the `Event`
  (Title→subject, Message + the same Commit/Service/Duration/deep-link lines `slackPayload` emits).
  A `smtpClient` type holding host/port/user/pass/from/to/tlsMode with a `Send(subject, body)` that:
  resolves + guards the host (`IsPrivate` unless `allowPrivate`), dials per `tlsMode` (implicit =
  `tls.Dial`; starttls = `Dial`+`StartTLS`; none = plain), `PlainAuth` when username set, then
  `Mail`/`Rcpt`/`Data`.
- **Dispatcher** — extend `resolved` (`dispatcher.go:91`) with the SMTP config; `resolve` fills it
  from env-first (new `cfg.NotifySMTP*`) then the decrypted row (`d.open` for the password). In
  `dispatch` (`dispatcher.go:151`) and `SendTest` (`dispatcher.go:208`), add an `if smtp configured`
  branch that renders + sends. `New` (`dispatcher.go:48`) gains the env-SMTP params and an
  `allowPrivate` flag.

## Phase 2 — Config + env overrides

Mirror the `VAC_NOTIFY_*` block (`config.go:125`, loaded `config.go:476`):
`VAC_NOTIFY_SMTP_HOST`, `_PORT`, `_USERNAME`, `_PASSWORD`, `_FROM`, `_TO`, `_TLS_MODE`,
`VAC_NOTIFY_SMTP_ALLOW_PRIVATE`. Env-set values win over the stored row, same as Discord/Slack.
`_PASSWORD` is env-only (never the config file), like the webhook URLs.

## Phase 3 — Handler + API

- **DTO** — extend `notificationSettingsDTO` (`handler/notifications.go:19`) with
  `smtp_host/port/username/from/to/tls_mode` (returned plaintext) and `smtp_password_configured`
  + `smtp_password_hint` (redacted via `last4`, like the URLs). The request struct
  (`putNotificationsRequest:49`) gains the same fields, with the password as `*string` patch
  semantics (nil = leave, "" = clear) routed through the existing `sealURLUpdate`
  (`handler/notifications.go:113`) — rename it mentally to "seal a secret field," it's already
  generic. `VAC_MASTER_KEY`-missing → 503 only when a password is being set (existing behaviour).
- No new route: `GET/PUT /settings/notifications` and `.../test` already carry it.

## Phase 4 — UI

- `notifications-section.tsx` — a new **Email** card/group above the event toggles: host, port,
  username, password (redacted-on-read via the `configuredPlaceholder` pattern), from, to,
  TLS-mode select (`starttls`/`implicit`/`none`). Reuse `ChannelField`'s "configured? show hint"
  shape for the password.
- Types (`ui/src/types/api.ts`) — extend `NotificationSettings` + `UpdateNotificationInput` with
  the SMTP fields. The existing "Send test" button (line 113) needs no change — it already pings
  every configured channel via `SendTest`.

## Out of scope (explicitly)

- **Multiple relays / per-event recipient routing** — one relay, one recipient set.
- **Rich HTML templating** — short text body (optional minimal HTML alternative); no template engine.
- **DKIM / bounce handling / queue with retry-to-disk** — fire-and-forget like the webhooks; a
  failed send is logged, not retried beyond the in-call attempt.
- **OAuth2 / XOAUTH2 SMTP auth** — `PlainAuth` (and unauthenticated) only; covers SMTP relays,
  app-passwords, and Gmail-with-app-password. Revisit if anyone needs Google OAuth.
- **A separate per-channel event toggle matrix** — the single shared `events` map governs all
  channels.

## New deviation to record (D10)

`docs/deviations.md`: the webhook SSRF guard is a hard private-address wall (D8 rationale: a webhook
URL must never reach internal space). For SMTP the same `netguard.IsPrivate` predicate is reused,
but as an **opt-out** (`VAC_NOTIFY_SMTP_ALLOW_PRIVATE`) because a legitimate relay can be a LAN/
sidecar MTA. Trade-off: an operator who sets the allow-flag can point SMTP at a private address —
acceptable, it's their own relay, explicitly enabled, and far narrower than a typo'd public webhook.

## Rough size

- Phase 1: 1 migration, ~6 row fields + store edits, 1 new `email.go` (~70 lines: renderer +
  guarded SMTP send), dispatcher `resolved`/`dispatch`/`SendTest`/`New` edits. Medium — the TLS-mode
  switch + guard is the real work; the renderer is trivial.
- Phase 2: ~8 env lines in config. Tiny.
- Phase 3: DTO + request struct fields, reuse `sealURLUpdate`. Small.
- Phase 4: 1 settings card, 2 type extensions. Small.

## Build order

1. Migration + `NotificationSettingsRow`/store edits.
2. `notify/email.go`: renderer + guarded `tlsMode`-aware `Send`; unit-test the renderer and the
   `IsPrivate` gate (mirror `notify_test.go`'s loopback handling).
3. Wire SMTP into `resolved`/`resolve`/`dispatch`/`SendTest`/`New`.
4. Config env block (`VAC_NOTIFY_SMTP_*`) + `main.go` `notify.New` call.
5. Handler DTO + request fields (reuse `sealURLUpdate` for the password).
6. UI Email card + types.
7. `/code-review` + `/simplify`; record **D10** in `docs/deviations.md`; `/refresh-kb` (the notify
   module + a migration changed → `architecture.md`).

## Verification

- A relay configured via Settings, `make dev`: "Send test" delivers an email and the toast count
  includes it; a deploy-failed event lands in the inbox with subject + body + deep link.
- Password round-trips redacted: after save, `GET /settings/notifications` returns
  `smtp_password_configured:true` + a `…last4` hint, never the secret.
- Env override wins: `VAC_NOTIFY_SMTP_HOST=...` overrides the stored row.
- Guard: pointing SMTP at `127.0.0.1` is refused unless `VAC_NOTIFY_SMTP_ALLOW_PRIVATE` is set.
- No `VAC_MASTER_KEY`: setting an SMTP password returns 503; host/from/to (no secret) still save.
- `make lint typecheck test` clean.
