# Shipping VAC to a VPS

How VAC is distributed and installed — the `curl -sSL get.vac.vojir.io | sh`
flow end to end. Two audiences:

- **[Maintainer](#part-1--maintainer-setup)** — publish images and host the
  install assets (one-time setup + per-release).
- **[Operator](#part-2--installing-on-a-vps)** — run VAC on their own VPS.

---

## How it works

The installer does **not** build from source on the VPS. The heavy build (UI →
embedded Go binary, Caddy image) happens once in CI and is published to a
container registry. The VPS only pulls prebuilt images and starts them:

```
git tag v0.5.0          ─►  GitHub Actions  ─►  ghcr.io/vojir/vac-api:0.5.0
                                                 ghcr.io/vojir/vac-proxy:0.5.0
                                  │
 curl get.vac.vojir.io | sh  ◄────┘ (pulls images)
        │
        ├─ ensures Docker
        ├─ writes /opt/vac/.env (generated secrets)
        ├─ fetches compose.prod.yaml
        └─ docker compose up -d   ─►  http://<ip>:3000  (onboarding wizard)
```

Relevant files in this repo:

| File | Role |
|---|---|
| `api/Dockerfile`, `proxy/Dockerfile` | Build the two images (UI is embedded into the API binary via the `embedui` tag). |
| `.github/workflows/release.yml` | Builds + pushes multi-arch images to GHCR on a `v*` tag. |
| `compose.prod.yaml` | Production stack — pulls `image:` refs instead of `build:`. |
| `scripts/install.sh` | The bootstrap script served at `get.vac.vojir.io`. |

---

## Part 1 — Maintainer setup

### 1.1 One-time: registry

Images publish to **GitHub Container Registry (GHCR)** — no pull rate limits on
public images (unlike Docker Hub), and CI authenticates with the built-in
`GITHUB_TOKEN`.

After the **first** successful release run, make both packages public (GHCR
creates them private by default — the installer pulls anonymously, so they
must be public or installs fail with `denied`/`unauthorized`):

1. GitHub → your profile/org → **Packages** → `vac-api`.
2. **Package settings** → **Change visibility** → **Public**.
3. Repeat for `vac-proxy`.

> If your GitHub owner is not `vojir`, the CI workflow auto-resolves it from
> `github.repository_owner`. Update the **default** in `compose.prod.yaml`
> (`VAC_REGISTRY`) and `scripts/install.sh` (`VAC_REGISTRY=`) to match, so the
> installer pulls from the right namespace.

### 1.2 One-time: host the install assets

`get.vac.vojir.io` must serve two files over HTTPS as plain text:

- `install.sh` at the root (`https://get.vac.vojir.io`)
- `compose.prod.yaml` (`https://get.vac.vojir.io/compose.prod.yaml`)

The simplest option is a small Caddy site (VAC itself can host it once it's up,
or any static host / GitHub Pages / release assets work too). Example Caddyfile:

```caddy
get.vac.vojir.io {
    root * /srv/vac-get
    @sh path / /install.sh
    header @sh Content-Type "text/x-shellscript; charset=utf-8"
    file_server
}
```

Put `scripts/install.sh` (as `install.sh`) and `compose.prod.yaml` in
`/srv/vac-get`. Re-copy them whenever they change (see automation below).

> Override for testing without DNS:
> `VAC_ASSET_BASE=https://raw.githubusercontent.com/<owner>/vac/main sh install.sh`

### 1.3 Cutting a release

```bash
git tag v0.5.0
git push origin v0.5.0
```

This triggers `.github/workflows/release.yml`, which builds **linux/amd64** and
**linux/arm64** images and pushes:

- `ghcr.io/<owner>/vac-api:0.5.0`, `:0.5`, `:latest`
- `ghcr.io/<owner>/vac-proxy:0.5.0`, `:0.5`, `:latest`

Then refresh the install assets so the served `install.sh` pins the new version
(set its `VAC_VERSION` default, or instruct operators to pass `VAC_VERSION`):

```bash
# example: publish to the static host
scp scripts/install.sh compose.prod.yaml server:/srv/vac-get/
```

Database migrations run automatically when `vac-api` boots the new image
(`api/internal/db/migrate.go`), so an upgrade is just a re-pull.

### 1.4 Security hardening (recommended before promoting publicly)

Piping a remote script to `sh` is a trust ask. Reduce the blast radius:

- **Serve only over HTTPS** and publish a checksum next to the script:
  ```bash
  sha256sum install.sh   # publish as install.sh.sha256
  ```
  Document the inspect-first path on your site:
  ```bash
  curl -sSL get.vac.vojir.io -o vac.sh
  less vac.sh
  sh vac.sh
  ```
- **Pin image digests** per release in `compose.prod.yaml`
  (`image: ghcr.io/vojir/vac-api@sha256:…`) so a registry compromise can't
  silently swap images. `VAC_VERSION` pins the tag; digests pin the bytes.
- Keep old `install.sh` versions reachable so existing automation doesn't break.

---

## Part 2 — Installing on a VPS

### Requirements

- A Linux VPS (amd64 or arm64), root or `sudo`.
- Ports **80** and **443** open (app ingress via Caddy) and **3000** for the
  dashboard until a domain is set.
- For automatic HTTPS subdomains later: a wildcard DNS record (see below).

### Install

```bash
curl -sSL get.vac.vojir.io | sh
```

or, to pin a version / pre-set a domain:

```bash
curl -sSL get.vac.vojir.io -o vac.sh
VAC_VERSION=v0.5.0 VAC_DOMAIN=vac.example.com sh vac.sh
```

The installer:

1. Checks the OS/arch and installs Docker (via `get.docker.com`) if missing.
2. Creates `/opt/vac/` and generates `/opt/vac/.env` with a random
   `VAC_MASTER_KEY` and DB password (**preserved on re-runs**).
3. Fetches `compose.prod.yaml` and runs `docker compose up -d`.
4. Installs the `vac` management command.
5. Prints the dashboard URL.

Open `http://<server-ip>:3000` and complete the **onboarding wizard** to create
your admin account.

### Installer environment variables

| Variable | Default | Purpose |
|---|---|---|
| `VAC_VERSION` | `latest` | Image tag to pull (pin to a release, e.g. `v0.5.0`). |
| `VAC_DOMAIN` | _(empty)_ | Set the base domain at install time (else add later). |
| `VAC_INSTALL_DIR` | `/opt/vac` | Where compose + `.env` live. |
| `VAC_REGISTRY` | `ghcr.io/vojir` | Image namespace. |
| `VAC_HOST_PORT` | `3000` | Host port for the dashboard. |
| `VAC_ASSET_BASE` | `https://get.vac.vojir.io` | Where to fetch assets from. |

### Adding a domain later

Automatic `https://{app}.vac.example.com` subdomains need a base domain. Set it
any time after install:

```bash
vac set-domain vac.example.com
```

DNS required (point at the VPS public IP):

```
A    vac.example.com      → <server-ip>
A    *.vac.example.com    → <server-ip>     # wildcard for app subdomains
```

Caddy provisions TLS automatically once DNS resolves. Custom per-service domains
are managed in the dashboard (Settings → domains) regardless of the base domain.

### The `vac` command

```
vac status               show running services
vac logs [service]       tail logs (e.g. vac logs vac-api)
vac upgrade [version]    pull + recreate (optionally pin: vac upgrade v0.6.0)
vac set-domain <domain>  enable automatic HTTPS subdomains
vac unset-domain         disable automatic subdomains
vac up | down | restart [service]
vac config               print /opt/vac/.env
```

### Upgrading

```bash
vac upgrade            # latest images for the pinned major/minor
vac upgrade v0.6.0     # jump to a specific release
```

Re-running the installer is equivalent and also safe — it preserves
`/opt/vac/.env`.

### Backups

The state worth backing up lives in Docker named volumes:

- `vac_db_data` — Postgres (apps, deployments, users, encrypted secrets)
- `caddy_data` — TLS certificates / ACME account
- `/opt/vac/.env` — **`VAC_MASTER_KEY`**; without it, encrypted env vars and
  notification secrets are unrecoverable. Store it somewhere safe.

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `denied` / `unauthorized` on pull | GHCR packages still private — make `vac-api` and `vac-proxy` public (§1.1). |
| `VAC_MASTER_KEY is required` | `.env` missing/empty — re-run the installer, or restore your backed-up `.env`. |
| Dashboard unreachable on `:3000` | Check `vac status` / `vac logs vac-api`; ensure the host firewall allows the port. |
| Subdomains have no certificate | DNS not resolving yet, or ports 80/443 blocked. Verify the `A`/wildcard records and `vac logs vac-proxy`. |
| `docker compose` not found | Old Docker — the installer needs the Compose v2 plugin (`docker compose`, not `docker-compose`). |
| Can't read the Docker socket | `DOCKER_GID` in `.env` doesn't match the host. Set it to `getent group docker \| cut -d: -f3` and `vac up`. |
