#!/bin/sh
# VAC installer — https://get.vac.vojir.io
#
#   curl -sSL get.vac.vojir.io | sh
#
# Idempotent: re-running upgrades images and preserves your secrets/config.
# Override defaults with env vars, e.g.:
#   VAC_VERSION=v0.5.0 VAC_DOMAIN=vac.example.com sh install.sh
set -eu

# ── Config (overridable via env) ───────────────────────────────────────────
VAC_VERSION="${VAC_VERSION:-latest}"
VAC_INSTALL_DIR="${VAC_INSTALL_DIR:-/opt/vac}"
VAC_REGISTRY="${VAC_REGISTRY:-ghcr.io/vojir-mikulas}"
VAC_ASSET_BASE="${VAC_ASSET_BASE:-https://get.vac.vojir.io}"
VAC_HOST_PORT="${VAC_HOST_PORT:-3000}"
VAC_DOMAIN="${VAC_DOMAIN:-}"
# When installed via sudo, also let the invoking user run `vac` without sudo:
# add them to the `docker` group and give them the install dir (which holds the
# root-only .env). Opt out with the --no-grant flag or VAC_GRANT_ACCESS=0.
# Note: docker-group membership is root-equivalent on this host — opt out if
# that isn't acceptable.
VAC_GRANT_ACCESS="${VAC_GRANT_ACCESS:-1}"
RELOGIN=0

COMPOSE_FILE="$VAC_INSTALL_DIR/compose.prod.yaml"
ENV_FILE="$VAC_INSTALL_DIR/.env"

# ── Output helpers ──────────────────────────────────────────────────────────
if [ -t 1 ]; then B="$(printf '\033[1m')"; G="$(printf '\033[32m')"; Y="$(printf '\033[33m')"; R="$(printf '\033[31m')"; N="$(printf '\033[0m')"; else B=; G=; Y=; R=; N=; fi
info()  { printf '%s==>%s %s\n' "$G" "$N" "$1"; }
warn()  { printf '%s!  %s%s\n' "$Y" "$1" "$N"; }
die()   { printf '%serror:%s %s\n' "$R" "$N" "$1" >&2; exit 1; }
usage() {
  cat <<USAGE
VAC installer

  curl -sSL get.vac.vojir.io | sudo sh
  curl -sSL get.vac.vojir.io/install.sh | sudo sh -s -- [flags]

Flags:
  --no-grant   Don't add the invoking user to the docker group or chown the
               install dir — leave VAC root-only.
  --grant      Force the grant on (this is the default).
  -h, --help   Show this help and exit.

Env overrides: VAC_VERSION, VAC_DOMAIN, VAC_INSTALL_DIR, VAC_HOST_PORT,
VAC_REGISTRY, VAC_GRANT_ACCESS (1/0).
USAGE
}

# Answer --help before any preflight/elevation so it never needs Linux or root.
# Peek without consuming "$@" so the args still forward through self-elevation.
for _a in "$@"; do
  case "$_a" in -h|--help) usage; exit 0 ;; esac
done

# ── Pre-flight ────────────────────────────────────────────────────────────--
[ "$(uname -s)" = "Linux" ] || die "VAC installs on Linux hosts only (found $(uname -s))."

case "$(uname -m)" in
  x86_64|amd64|aarch64|arm64) ;;
  *) die "Unsupported architecture: $(uname -m). VAC ships amd64 and arm64." ;;
esac

if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    info "Re-running with sudo…"
    # Forward "$@" so flags (e.g. --no-grant) survive the elevation: with
    # `sh -c BODY name arg…`, name becomes $0 and the rest become $1….
    exec sudo -E sh -c "$(cat "$0" 2>/dev/null || true)" sh "$@" 2>/dev/null || die "Please run as root: curl -sSL get.vac.vojir.io | sudo sh"
  fi
  die "Please run as root (or install sudo)."
fi

# ── Args ──────────────────────────────────────────────────────────────────--
# Parsed after self-elevation (which forwards "$@") so flags reliably cross the
# sudo boundary rather than depending on an env var surviving it. Flags win
# over the env defaults set above.
while [ $# -gt 0 ]; do
  case "$1" in
    --no-grant) VAC_GRANT_ACCESS=0 ;;
    --grant)    VAC_GRANT_ACCESS=1 ;;
    -h|--help)  usage; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

fetch() {
  # fetch <url> <dest>
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else die "Neither curl nor wget is available."; fi
}

rand_hex() {
  # rand_hex <bytes>
  if command -v openssl >/dev/null 2>&1; then openssl rand -hex "$1"
  else od -An -tx1 -N "$1" /dev/urandom | tr -d ' \n'; fi
}

write_vac_cli() {
  # Embedded management CLI → /usr/local/bin/vac. __VAC_DIR__ and
  # __VAC_ASSET_BASE__ are substituted after the (unexpanded) heredoc is
  # written, so the script body itself stays free of shell expansion.
  cat > /usr/local/bin/vac <<'VACEOF'
#!/bin/sh
set -eu
DIR="__VAC_DIR__"
ASSET_BASE="__VAC_ASSET_BASE__"
COMPOSE="$DIR/compose.prod.yaml"
ENVF="$DIR/.env"
dc() { docker compose -f "$COMPOSE" --env-file "$ENVF" "$@"; }
set_env() {
  if grep -q "^$1=" "$ENVF" 2>/dev/null; then sed -i "s|^$1=.*|$1=$2|" "$ENVF"
  else printf '%s=%s\n' "$1" "$2" >> "$ENVF"; fi
}
cmd="${1:-help}"; [ $# -gt 0 ] && shift || true
case "$cmd" in
  up)        dc up -d "$@" ;;
  down)      dc down "$@" ;;
  restart)   dc restart "$@" ;;
  status|ps) dc ps "$@" ;;
  logs)      dc logs -f --tail=100 "$@" ;;
  pull)      dc pull "$@" ;;
  upgrade)   [ $# -gt 0 ] && set_env VAC_VERSION "$1" || true; dc pull && dc up -d ;;
  set-domain)
    [ $# -ge 1 ] || { echo "usage: vac set-domain <domain>" >&2; exit 1; }
    set_env VAC_BASE_DOMAIN "$1"; dc up -d vac-api
    printf 'Base domain set to %s.\n' "$1"
    printf 'Dashboard will be reachable at:  https://vac.%s\n' "$1"
    printf 'DNS records to create:\n'
    printf '  A   vac.%s   → this host\n' "$1"
    printf '  A   *.%s     → this host   (for deployed apps)\n' "$1"
    printf 'TLS certificates are issued automatically by Let'"'"'s Encrypt once DNS points here.\n' ;;
  unset-domain) set_env VAC_BASE_DOMAIN ""; dc up -d vac-api; echo "Automatic subdomains disabled." ;;
  config)    cat "$ENVF" ;;
  version|--version|-v)
    pinned="$(grep '^VAC_VERSION=' "$ENVF" 2>/dev/null | cut -d= -f2-)"
    printf 'pinned: %s\n' "${pinned:-unset}"
    if dc ps --status=running --services 2>/dev/null | grep -q '^vac-api$'; then
      dc exec -T vac-api vac-api version 2>/dev/null || echo 'running: vac-api unreachable'
    else
      echo 'running: vac-api not up'
    fi ;;
  reset-password)
    [ $# -ge 1 ] || { echo "usage: vac reset-password <username>" >&2; exit 1; }
    dc ps --status=running --services 2>/dev/null | grep -q '^vac-api$' \
      || { echo "vac-api is not running; start it with 'vac up' first." >&2; exit 1; }
    dc exec vac-api vac-api reset-password "$@" ;;
  uninstall)
    # Prefer an on-disk copy so air-gapped hosts work; fall back to fetching
    # the published asset. uninstall.sh exits 0 if the user declines.
    [ "$(id -u)" -eq 0 ] || { echo "vac uninstall must run as root" >&2; exit 1; }
    if [ -x "$DIR/uninstall.sh" ]; then
      exec "$DIR/uninstall.sh" "$@"
    elif command -v curl >/dev/null 2>&1; then
      curl -fsSL "$ASSET_BASE/uninstall.sh" | sh -s -- "$@"
    elif command -v wget >/dev/null 2>&1; then
      wget -qO- "$ASSET_BASE/uninstall.sh" | sh -s -- "$@"
    else
      echo "neither curl nor wget available, and no $DIR/uninstall.sh on disk" >&2
      exit 1
    fi ;;
  *)
    cat <<USAGE
vac — manage this VAC install ($DIR)

  vac status                       show running services
  vac version                      show the running and pinned versions
  vac logs [service]               tail logs
  vac upgrade [version]            pull + recreate (optionally pin a version)
  vac set-domain <domain>          serve dashboard on HTTPS + enable app subdomains
  vac unset-domain                 disable HTTPS dashboard and app subdomains
  vac reset-password <username>    set a new password and revoke sessions
  vac up | down | restart [service]
  vac config                       print the .env
  vac uninstall [--purge] [--apps] [--backup DIR] [--yes]
                                   remove VAC; see --help for full options
USAGE
    ;;
esac
VACEOF
  sed -i "s#__VAC_DIR__#$VAC_INSTALL_DIR#g" /usr/local/bin/vac
  sed -i "s#__VAC_ASSET_BASE__#$VAC_ASSET_BASE#g" /usr/local/bin/vac
  chmod +x /usr/local/bin/vac
}

grant_user_access() {
  # Optionally let the user who invoked `sudo` manage VAC without sudo: add
  # them to the `docker` group (so docker/compose calls don't need root) and
  # hand them the install dir (so the `vac` CLI can read the root-only .env).
  # No-op when run as a real root login (no $SUDO_USER) or when disabled.
  [ "$VAC_GRANT_ACCESS" = "1" ] || return 0
  TARGET_USER="${SUDO_USER:-}"
  [ -n "$TARGET_USER" ] && [ "$TARGET_USER" != "root" ] || return 0
  command -v usermod >/dev/null 2>&1 || return 0

  if getent group docker >/dev/null 2>&1; then
    if id -nG "$TARGET_USER" 2>/dev/null | tr ' ' '\n' | grep -qx docker; then
      : # already a member
    else
      info "Adding ${B}${TARGET_USER}${N} to the 'docker' group (root-equivalent; VAC_GRANT_ACCESS=0 to skip)…"
      if usermod -aG docker "$TARGET_USER"; then
        RELOGIN=1
      else
        warn "  could not add $TARGET_USER to the docker group"
      fi
    fi
  fi

  info "Giving ${B}${TARGET_USER}${N} ownership of ${VAC_INSTALL_DIR} (so 'vac' reads .env without sudo)…"
  chown -R "$TARGET_USER" "$VAC_INSTALL_DIR" || warn "  could not chown $VAC_INSTALL_DIR"
}

# ── Docker ────────────────────────────────────────────────────────────────--
if ! command -v docker >/dev/null 2>&1; then
  info "Docker not found — installing via get.docker.com…"
  if command -v curl >/dev/null 2>&1; then curl -fsSL https://get.docker.com | sh
  else die "curl is required to install Docker."; fi
fi

# Ensure the daemon is up (systemd or sysvinit).
if command -v systemctl >/dev/null 2>&1; then
  systemctl enable --now docker >/dev/null 2>&1 || true
elif command -v service >/dev/null 2>&1; then
  service docker start >/dev/null 2>&1 || true
fi

docker info >/dev/null 2>&1 || die "Docker is installed but the daemon isn't reachable."
docker compose version >/dev/null 2>&1 || die "The Docker Compose v2 plugin is required (docker compose)."

# ── Lay down the install directory ────────────────────────────────────────--
info "Installing into ${B}${VAC_INSTALL_DIR}${N}"
mkdir -p "$VAC_INSTALL_DIR"

info "Fetching compose.prod.yaml…"
fetch "$VAC_ASSET_BASE/compose.prod.yaml" "$COMPOSE_FILE"

# Stash the uninstall script next to the compose file so `vac uninstall` works
# offline and shows up where an operator would look for it. Non-fatal — the
# `vac uninstall` wrapper also fetches it on demand.
info "Fetching uninstall.sh…"
if fetch "$VAC_ASSET_BASE/uninstall.sh" "$VAC_INSTALL_DIR/uninstall.sh"; then
  chmod +x "$VAC_INSTALL_DIR/uninstall.sh"
else
  warn "Could not fetch uninstall.sh; 'vac uninstall' will fall back to the network."
fi

# ── Generate .env (only on first install — secrets are preserved on re-run) ──
if [ -f "$ENV_FILE" ]; then
  FRESH=0
  info "Existing config found — keeping secrets, upgrading images."
else
  FRESH=1
  info "Generating secrets…"
  MASTER_KEY="$(rand_hex 32)"
  DB_PASSWORD="$(rand_hex 24)"          # hex → URL-safe inside the Postgres DSN

  DOCKER_GID="$(getent group docker 2>/dev/null | cut -d: -f3)"
  [ -n "${DOCKER_GID:-}" ] || DOCKER_GID="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 999)"

  umask 077
  cat > "$ENV_FILE" <<EOF
# Generated by the VAC installer on $(date -u +%Y-%m-%dT%H:%M:%SZ). Keep this safe.
VAC_VERSION=$VAC_VERSION
VAC_REGISTRY=$VAC_REGISTRY
VAC_MASTER_KEY=$MASTER_KEY
VAC_DB_PASSWORD=$DB_PASSWORD
DOCKER_GID=$DOCKER_GID
VAC_HOST_PORT=$VAC_HOST_PORT
VAC_BASE_DOMAIN=$VAC_DOMAIN
EOF
  chmod 600 "$ENV_FILE"
fi

# Pin the requested version for this run even on upgrades.
if grep -q '^VAC_VERSION=' "$ENV_FILE"; then
  sed -i "s|^VAC_VERSION=.*|VAC_VERSION=$VAC_VERSION|" "$ENV_FILE"
else
  printf 'VAC_VERSION=%s\n' "$VAC_VERSION" >> "$ENV_FILE"
fi

# ── Start the stack ─────────────────────────────────────────────────────────
info "Pulling images (${VAC_REGISTRY}/vac-* : ${VAC_VERSION})…"
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" pull

info "Starting VAC…"
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d

# ── Install the `vac` management CLI ─────────────────────────────────────────
info "Installing the 'vac' command…"
write_vac_cli

# Hand sudo-free access to the invoking user (opt-out via VAC_GRANT_ACCESS=0).
grant_user_access

# ── Done ──────────────────────────────────────────────────────────────────--
IP="$(curl -fsS https://api.ipify.org 2>/dev/null || true)"
[ -n "$IP" ] || IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$IP" ] || IP="<server-ip>"

# On a fresh install, vac-api writes a one-time setup token into its work dir.
# Read it back through the container so we can hand the operator a ready-to-click
# link with the token baked in — no log-digging required. The token lives on a
# named volume, so the host can't read the file directly. Upgrades have no token
# (the admin already exists), so we only bother when FRESH=1.
SETUP_TOKEN=""
if [ "${FRESH:-0}" = "1" ]; then
  info "Waiting for VAC to come up…"
  i=0
  while [ "$i" -lt 60 ]; do
    SETUP_TOKEN="$(docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" \
      exec -T vac-api cat /var/lib/vac/repos/setup.token 2>/dev/null | tr -d '\r\n' || true)"
    [ -n "$SETUP_TOKEN" ] && break
    i=$((i + 1)); sleep 1
  done
fi

printf '\n%s VAC is up.%s\n\n' "$B$G" "$N"
if [ -n "$VAC_DOMAIN" ]; then
  printf '  Dashboard:  %shttps://vac.%s%s   (once DNS + TLS settle)\n' "$B" "$VAC_DOMAIN" "$N"
  printf '  Direct:     http://%s:%s   (recovery / pre-DNS fallback)\n' "$IP" "$VAC_HOST_PORT"
  printf '\n  DNS: point %sA vac.%s%s and %sA *.%s%s at this host (%s).\n' "$B" "$VAC_DOMAIN" "$N" "$B" "$VAC_DOMAIN" "$N" "$IP"
else
  printf '  Dashboard:  %shttp://%s:%s%s\n' "$B" "$IP" "$VAC_HOST_PORT" "$N"
  printf '\n  Add a domain later to put the dashboard on HTTPS and enable\n'
  printf '  automatic app subdomains:\n'
  printf '    %svac set-domain example.com%s\n' "$B" "$N"
fi
if [ -n "$SETUP_TOKEN" ]; then
  printf '\n  %sCreate your admin account — open this link (token included):%s\n' "$B" "$N"
  printf '    %s%shttp://%s:%s/setup?token=%s%s\n' "$B" "$G" "$IP" "$VAC_HOST_PORT" "$SETUP_TOKEN" "$N"
  printf '\n  This one-time token is consumed once the account is created.\n'
else
  printf '\n  Open the dashboard to create your admin account.\n'
fi
printf '  Manage:  %svac status | vac logs | vac upgrade | vac down%s\n' "$B" "$N"
if [ "${RELOGIN:-0}" = "1" ]; then
  printf '\n  %sLog out and back in%s (or run %snewgrp docker%s) so %s%s%s can run\n' "$B" "$N" "$B" "$N" "$B" "${SUDO_USER:-your user}" "$N"
  printf '  %svac%s commands without sudo.\n' "$B" "$N"
fi
printf '\n'
