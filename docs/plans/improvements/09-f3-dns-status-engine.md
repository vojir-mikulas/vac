# 09·F3 — The DNS + cert status engine

**Parent:** [09 — Vercel-like domain management](09-domains-lifecycle-overhaul.md), step
**F3**. That step was one paragraph in the parent ("replace `cert_status` with one derived
status, run a lightweight background reconciler"). It is not plumbing — it is a new
always-on subsystem doing outbound DNS + TLS observation for every domain, and it collides
with **F1** (which removes the `type='auto'` rows that a per-domain status column would live
in). This doc designs it properly so F1 and F3 stop fighting.

**Read F1 first.** F3 assumes F1's "derive auto-subdomains, don't store them" lands. The two
key decisions below (§1, §2) only make sense in that light.

---

## The two decisions the parent left open

### §1 — Status is an in-memory projection, not a column (resolves the F1 collision)

The parent's F3 wants "one honest status per domain, including auto hosts." F1 deletes the
auto rows. A stored status column therefore can't cover auto hosts — and that tension is the
single thing that made F1+F3 feel at odds.

**Decision: the derived status is never stored. It is a pure runtime projection, computed by
an in-memory engine, for *all* hosts — custom and derived-auto alike.** The database keeps
only what genuinely needs to survive a restart, which is the cert-expiry de-dupe state the
notification job already owns (`cert_not_after`, `cert_expiry_notified_at`).

Why this is the right call, not just a convenient one:

- **It dissolves the F1 collision.** Auto hosts losing their row costs nothing, because
  status was never going to live in a row. The engine enumerates auto hosts the same way
  F1's reconcile does (the derived-host function over `app slug × HTTP services × base
  domain`) and assigns them a status in the same map as custom domains.
- **The thing it replaces was already a lie.** `cert_status` is set to `active` *never* and
  to `error` only on a route-push failure (`manager.go:243`). It is not a value worth
  migrating — it is a value worth deleting.
- **Status is cheap to recompute and stale-by-nature.** DNS and "is a cert being served"
  are live external facts. Persisting them buys a few seconds of cold-start warmth in
  exchange for write amplification (every poll) and a second source of truth that can
  disagree with reality. Not worth it.

**Migration:** drop the `cert_status` column; delete `SetCertStatus`, the `CertStatus*`
constants, and the `CertStatus` field on `store.Domain`. The route-push failure that used to
call `SetCertStatus(error)` instead calls `engine.SetError(hostname, reason)` (in-memory).
Document the drop in `docs/deviations.md`.

> **Cold start:** before the first reconcile pass completes (~seconds after boot), every
> host reports the transient `checking` state (§3). The UI renders it as a neutral spinner,
> not a red badge. No persisted status means no "remembered green that's actually now broken"
> footgun.

### §2 — Resolver semantics (the caveat the parent hand-waved)

`/dns-check` uses `net.DefaultResolver` (`instance.go:194`), which on a typical VPS goes
through the local stub/systemd-resolved cache. That cache **respects TTL and won't see a
freshly-changed record until the old one expires** — so a domain the operator just pointed
here can read `awaiting_dns` for minutes, and a manual "Refresh" won't help. If we ship that
silently, every user's first impression is "I set the record and VAC still says invalid."

**Decision: the status engine uses an injectable `*net.Resolver`, defaulting to a public
recursive resolver (e.g. `1.1.1.1`/`8.8.8.8`) dialed directly, not the system stub.** A
public recursive resolver still honours authoritative TTL but bypasses the box's local
cache, so it sees changes as soon as the operator's DNS provider serves them. The resolver
is configurable (operator can force the system resolver if egress to public DNS is blocked).

- Keep `/dns-check`'s existing behaviour for its one-shot button, but point both at the same
  injectable resolver so they agree.
- This does **not** make us a DNS provider and adds no record types — it is purely *reading*
  A/CNAME with fresher cache behaviour. Note it in `docs/deviations.md` alongside the
  "A/CNAME only" stance from the parent.
- Honest limit to document: we still cannot beat authoritative TTL. The config card (Phase
  1) should say "DNS changes can take up to your record's TTL to show here," so a slow flip
  reads as expected, not broken.

---

## §3 — The status model

Runtime states. Only the first five are surfaced as domain status in the parent's table; the
sixth is a transient pre-result state.

| State | Meaning | How the engine decides it |
|--------|---------|---------------------------|
| `checking` | no observation yet this lifetime (cold start / just added) | initial value before first probe returns |
| `awaiting_dns` | hostname does not resolve to this VPS | DNS lookup empty / NXDOMAIN, or resolves nowhere useful |
| `misconfigured` | resolves, but wrong target / wrong record type | resolves but not to the VPS IP; or CNAME-at-apex; carries `detail` |
| `issuing` | points here, no served cert observed yet | DNS valid **and** SNI probe served no leaf (or not-yet-valid) |
| `active` | DNS valid **and** a leaf cert is served with `NotAfter` in the future | DNS valid **and** SNI probe returned a future `NotAfter` |
| `error` | route push failed / issuance failed | set imperatively by the proxy manager; carries `detail` |

Every entry also carries: `detail string` (the human reason for `misconfigured`/`error`),
`cert_not_after *time.Time` (when known), and `last_checked time.Time`.

### Detection logic, per host

1. **Classify the host** as apex vs subdomain. "Apex" = the host equals its registrable
   domain (eTLD+1). Use `golang.org/x/net/publicsuffix` (`publicsuffix.EffectiveTLDPlusOne`)
   so `example.co.uk` is treated as apex, not `co.uk`. (New dependency — small, std-adjacent;
   note it. Fallback heuristic "≤2 labels or == base domain" is wrong for multi-part TLDs,
   so prefer the lib.)
2. **Resolve** via the injectable resolver (§2):
   - **Apex** → expect an `A` to the VPS IP. If a `CNAME` is present at the apex →
     `misconfigured`, detail `"CNAME at apex is invalid — use an A record to <ip>"`.
   - **Subdomain** → accept `CNAME` to the base host **or** `A` to the VPS IP.
   - No records / NXDOMAIN → `awaiting_dns`.
   - Resolves but not to the VPS IP → `misconfigured`, detail
     `"resolves to <ip> — expected <vps-ip>"`.
3. **If DNS is valid → cert probe** (SNI dial, §4): future `NotAfter` ⇒ `active` (+ record
   `cert_not_after`); no leaf / past ⇒ `issuing`.
4. **`error`** is never inferred here; it is pushed in by the proxy manager (`SetError`) and
   cleared on the next successful route push, so DNS-truth and push-truth don't overwrite
   each other.

---

## §4 — Reusing the cert probe (don't build a second one)

`certcheck` already SNI-dials `vac-proxy:443` with per-host SNI and reads the leaf
`NotAfter` (`certcheck.go:170`, `tlsProbe`). It runs **once a day** for expiry alerts. F3
needs the *same observation* much more often, to flip `issuing → active`.

**Decision: extract the SNI probe into a shared helper both consume.** The cleanest move is
a tiny `certprobe` package (or export `certcheck`'s `Probe`/`tlsProbe`) exposing
`func(ctx, host) (time.Time, error)`. Then:

- **`certcheck` keeps sole ownership of notifications** and the durable de-dupe stamps. F3
  does **not** touch `cert_expiry_notified_at` or fire alerts.
- **The engine owns frequent status observation.** It may *opportunistically* write the
  observed `NotAfter` via `store.SetCertNotAfter` for custom domains (keeps the daily job's
  data fresher at no extra cost), but this is optional and write-rate-limited; it is not
  required for status.

This keeps a single TLS-observation implementation and a clean split: **engine = "is a cert
being served right now" (status); certcheck = "is a served cert about to expire" (alerts).**

---

## §5 — The engine: scheduling, concurrency, caching

New package `api/internal/domainstatus` (background reconciler + in-memory store), wired in
`main.go` like `certcheck` and the proxy manager.

- **State:** `map[hostname]Status` behind a `sync.RWMutex`. The reader (API) takes a read
  lock and copies; the reconciler takes a write lock per-host on update.
- **Host set per round** = all `type='custom'` domains (`ListAllDomains`) **+** all derived
  auto hosts (the same enumeration F1's reconcile uses). Hosts that vanish from the set are
  evicted from the map.
- **Cadence (tiered, so we watch the interesting ones closely without hammering DNS):**
  - non-`active` hosts (`checking`/`awaiting_dns`/`misconfigured`/`issuing`/`error`):
    re-check every **~30s**.
  - `active` hosts: re-check every **~5min** (catches a cert that stopped renewing or DNS
    that broke).
- **Bounded concurrency:** worker pool / semaphore of **~4–8** so a box with many domains
  doesn't open a burst of DNS+TLS dials. Each host probe is `ctx`-bounded (DNS ~4s like
  `/dns-check`, TLS ~10s like `certcheck`).
- **Short per-host cache** (~15s): backs the manual "Refresh" affordance without letting an
  impatient operator hammer DNS — a refresh inside the window returns the cached result; a
  forced refresh outside it re-probes one host immediately.
- **Lifecycle:** `Run(ctx)` with an `InitialDelay` (~a few seconds; the proxy must be up to
  probe certs) mirroring `certcheck`'s shape; exits on `ctx` cancel (graceful shutdown).

### API surface

- Fold status into the domains list DTO (Phase 1 already renders the list): each row carries
  `{ state, detail, cert_not_after, last_checked }`. Auto hosts get a status too (read-only).
- `POST /api/.../domains/{id}/refresh` (or `?host=` for auto hosts, which have no id) → force
  one immediate re-probe, return the fresh status.
- **Baseline UI = poll** the list/status endpoint while any visible domain is not `active`
  (the parent's "auto-poll, not a one-shot button"). **Optional upgrade:** push status
  transitions over the existing WS hub so the badge flips with no client poll — nicer, but
  not required for acceptance.

### Proxy-manager touchpoint

`applyApp` (`manager.go:241-244`) currently calls `SetCertStatus(error)` on `PutRoute`
failure. Replace with `engine.SetError(hostname, err.Error())`; on a subsequent successful
push, clear it (`engine.ClearError(hostname)`) so a transient push failure self-heals once
the route lands. This is the *only* path that produces `error` — DNS/cert truth can't
overwrite an operator-visible push failure and vice-versa.

---

## Acceptance criteria

- A custom domain not yet pointed shows `awaiting_dns`; once DNS points at the VPS it flips
  to `active` **on its own** within one poll interval — observed against a resolver that does
  **not** require the local cache to expire first (§2).
- Apex-with-CNAME, subdomain-with-wrong-IP, and apex-with-correct-A are classified
  `misconfigured` / `misconfigured` / (→`issuing`→`active`) respectively, each with a
  human `detail`.
- Auto (derived) hosts show a live status in the list with **no `domains` row** backing them
  (proves §1 — works post-F1).
- A route-push failure surfaces as `error` with the push error text, and clears on the next
  successful push.
- No `cert_status` column remains; expiry notifications (certcheck) still work unchanged.

## Verification

- **Unit (engine, mock resolver + mock probe):** apex-A-correct → `active`; apex-CNAME →
  `misconfigured` (CNAME-at-apex detail); subdomain-CNAME-to-base → valid; subdomain-A-to-
  wrong-IP → `misconfigured`; NXDOMAIN → `awaiting_dns`; DNS-valid + probe-no-cert →
  `issuing`; DNS-valid + probe-future-NotAfter → `active`. `SetError`/`ClearError`
  precedence over DNS truth.
- **Unit (classifier):** `publicsuffix` apex detection for `example.com`, `app.example.com`,
  `example.co.uk`, `a.b.example.co.uk`.
- **Unit (shared probe):** the extracted `certprobe` is the same one `certcheck` uses (no
  behavioural drift); tiered cadence picks the right interval per state; per-host cache
  short-circuits a refresh inside the window and forces one outside it.
- **Integration:** point a test host's DNS (mock resolver returning the VPS IP) → engine
  reports `active` after a cert is served; break it → returns to `misconfigured`.
- `make test`, `make typecheck`, `make lint`.
- **Manual:** add a domain, point it, watch the badge flip without clicking; break the record,
  watch it go `misconfigured` with the resolved-vs-expected detail.

## Cross-refs / non-goals

- **Depends on F1** (derived auto hosts; shared host-enumeration function) and the parent's
  host-IP source (plan **01.4**). Builds on the `/dns-check` resolver (`instance.go:184`) and
  the cert probe (`certcheck.go:170`, migration **00033**).
- Touchpoints: `manager.go:241` (push-error → engine), `store/domains.go` (drop
  `cert_status`), `main.go` (wire the engine like `certcheck`).
- **Non-goals (inherited):** no nameserver delegation, no TXT verification, no new DNS record
  types — we only *read* A/CNAME. Status persistence is deliberately omitted (§1).
- **New dependency:** `golang.org/x/net/publicsuffix` (apex classification).
