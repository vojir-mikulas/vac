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

COMPOSE_FILE="$VAC_INSTALL_DIR/compose.prod.yaml"
ENV_FILE="$VAC_INSTALL_DIR/.env"

# ── Output helpers ──────────────────────────────────────────────────────────
if [ -t 1 ]; then B="$(printf '\033[1m')"; G="$(printf '\033[32m')"; Y="$(printf '\033[33m')"; R="$(printf '\033[31m')"; N="$(printf '\033[0m')"; else B=; G=; Y=; R=; N=; fi
info()  { printf '%s==>%s %s\n' "$G" "$N" "$1"; }
warn()  { printf '%s!  %s%s\n' "$Y" "$1" "$N"; }
die()   { printf '%serror:%s %s\n' "$R" "$N" "$1" >&2; exit 1; }

# ── Pre-flight ────────────────────────────────────────────────────────────--
[ "$(uname -s)" = "Linux" ] || die "VAC installs on Linux hosts only (found $(uname -s))."

case "$(uname -m)" in
  x86_64|amd64|aarch64|arm64) ;;
  *) die "Unsupported architecture: $(uname -m). VAC ships amd64 and arm64." ;;
esac

if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    info "Re-running with sudo…"
    exec sudo -E sh -c "$(cat "$0" 2>/dev/null || true)" 2>/dev/null || die "Please run as root: curl -sSL get.vac.vojir.io | sudo sh"
  fi
  die "Please run as root (or install sudo)."
fi

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
    echo "Base domain set to $1. Point A *.$1 and A $1 at this host." ;;
  unset-domain) set_env VAC_BASE_DOMAIN ""; dc up -d vac-api; echo "Automatic subdomains disabled." ;;
  config)    cat "$ENVF" ;;
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
  vac logs [service]               tail logs
  vac upgrade [version]            pull + recreate (optionally pin a version)
  vac set-domain <domain>          enable automatic HTTPS subdomains
  vac unset-domain                 disable automatic subdomains
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
  info "Existing config found — keeping secrets, upgrading images."
else
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

# ── Done ──────────────────────────────────────────────────────────────────--
IP="$(curl -fsS https://api.ipify.org 2>/dev/null || true)"
[ -n "$IP" ] || IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$IP" ] || IP="<server-ip>"

printf '\n%s VAC is up.%s\n\n' "$B$G" "$N"
if [ -n "$VAC_DOMAIN" ]; then
  printf '  Dashboard:  %shttps://%s%s   (once DNS + TLS settle)\n' "$B" "$VAC_DOMAIN" "$N"
  printf '  Direct:     http://%s:%s\n' "$IP" "$VAC_HOST_PORT"
  printf '\n  DNS: point %sA *.%s%s and %sA %s%s at this host (%s).\n' "$B" "$VAC_DOMAIN" "$N" "$B" "$VAC_DOMAIN" "$N" "$IP"
else
  printf '  Dashboard:  %shttp://%s:%s%s\n' "$B" "$IP" "$VAC_HOST_PORT" "$N"
  printf '\n  Add a domain later for automatic HTTPS subdomains:\n'
  printf '    %svac set-domain vac.example.com%s\n' "$B" "$N"
fi
printf '\n  Open the dashboard to create your admin account.\n'
printf '  Manage:  %svac status | vac logs | vac upgrade | vac down%s\n\n' "$B" "$N"
