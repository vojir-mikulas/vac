#!/bin/sh
# VAC installer — https://get.vac.vojir.io
#
#   curl -sSL get.vac.vojir.io | sh
#
# Run it in a terminal and it walks you through a short setup (domain, managed
# services, sudo-free access) and shows a summary before touching the host. Pipe
# it without a terminal (CI, provisioning) and it runs unattended with safe
# defaults. Re-running upgrades the images and preserves your secrets/config.
#
# Override any default — and skip the matching question — with env vars:
#   VAC_VERSION=v0.5.0 VAC_DOMAIN=vac.example.com sh install.sh
#
# Flags:
#   --yes, -y         Accept defaults; never prompt (even in a terminal).
#   --no-grant        Keep VAC root-only (don't grant the sudo user access).
#   --grant           Force the grant on (the default).
#   -h, --help        Show help and exit.
set -eu

# ─────────────────────────────────────────────────────────────────────────────
# Config (overridable via env)
# ─────────────────────────────────────────────────────────────────────────────

# Which knobs did the caller pre-set via env? A pre-set knob skips its wizard
# question — this is what keeps `curl | sh` fully unattended for automation.
# Captured before defaults are applied (defaults would mask the distinction).
[ -n "${VAC_DOMAIN+x}" ]           && DOMAIN_PRESET=1  || DOMAIN_PRESET=0
[ -n "${VAC_MANAGED_SERVICES+x}" ] && MANAGED_PRESET=1 || MANAGED_PRESET=0
[ -n "${VAC_GRANT_ACCESS+x}" ]     && GRANT_PRESET=1   || GRANT_PRESET=0

VAC_VERSION="${VAC_VERSION:-latest}"
VAC_INSTALL_DIR="${VAC_INSTALL_DIR:-/opt/vac}"
VAC_REGISTRY="${VAC_REGISTRY:-ghcr.io/vojir-mikulas}"
VAC_ASSET_BASE="${VAC_ASSET_BASE:-https://get.vac.vojir.io}"
VAC_HOST_PORT="${VAC_HOST_PORT:-3000}"
VAC_DOMAIN="${VAC_DOMAIN:-}"
# Managed services (backups, managed databases, the add-on catalog). Off by
# default — it starts background workers and uses a little more RAM. Toggle any
# time with `vac managed-services on|off`.
VAC_MANAGED_SERVICES="${VAC_MANAGED_SERVICES:-}"
# When installed via sudo, also let the invoking user run `vac` without sudo:
# add them to the `docker` group and give them the install dir (which holds the
# root-only .env). Opt out with --no-grant or VAC_GRANT_ACCESS=0.
# Note: docker-group membership is root-equivalent on this host.
VAC_GRANT_ACCESS="${VAC_GRANT_ACCESS:-1}"

ASSUME_YES=0          # --yes / -y: never prompt
INTERACTIVE=0         # resolved at wizard time (a readable /dev/tty + not --yes)
FRESH=0               # 1 on a first-time install (no existing .env)
RELOGIN=0             # 1 when the user must re-login for docker-group membership
IP=""                 # detected public/host IP, filled lazily by detect_ip()

COMPOSE_FILE="$VAC_INSTALL_DIR/compose.prod.yaml"
ENV_FILE="$VAC_INSTALL_DIR/.env"

# ─────────────────────────────────────────────────────────────────────────────
# Output & prompt helpers
# ─────────────────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  B="$(printf '\033[1m')"; D="$(printf '\033[2m')"; N="$(printf '\033[0m')"
  R="$(printf '\033[31m')"; G="$(printf '\033[32m')"; Y="$(printf '\033[33m')"; C="$(printf '\033[36m')"
else B=; D=; N=; R=; G=; Y=; C=; fi
info() { printf '%s==>%s %s\n' "$G" "$N" "$1"; }
warn() { printf '%s!  %s%s\n' "$Y" "$1" "$N"; }
die()  { printf '%serror:%s %s\n' "$R" "$N" "$1" >&2; exit 1; }

# Prompts read from /dev/tty, never stdin — under `curl | sh`, stdin is the
# pipe carrying this script, so only the controlling terminal can answer.
say() { printf '%s\n' "$1" > /dev/tty; }            # a line of wizard prose

ask() {
  # ask <question> [default] -> echoes the answer (default if blank)
  _def="${2:-}"
  if [ -n "$_def" ]; then printf '%s [%s] ' "$1" "$_def" > /dev/tty
  else printf '%s ' "$1" > /dev/tty; fi
  IFS= read -r _ans < /dev/tty || _ans=
  [ -n "$_ans" ] || _ans="$_def"
  printf '%s' "$_ans"
}

confirm() {
  # confirm <question> <default:y|n> -> 0 for yes, 1 for no
  _hint='[y/N]'; [ "${2:-n}" = y ] && _hint='[Y/n]'
  printf '%s %s ' "$1" "$_hint" > /dev/tty
  IFS= read -r _ans < /dev/tty || _ans=
  [ -n "$_ans" ] || _ans="${2:-n}"
  case "$_ans" in [Yy]*) return 0 ;; *) return 1 ;; esac
}

normalize_bool() {
  # Map a loose truthy/falsy string to the literal true|false the API expects.
  case "$1" in true|TRUE|1|yes|YES|on|y|Y) printf 'true' ;; *) printf 'false' ;; esac
}

usage() {
  cat <<USAGE
VAC installer

  curl -sSL get.vac.vojir.io | sudo sh
  curl -sSL get.vac.vojir.io/install.sh | sudo sh -s -- [flags]

Flags:
  --yes, -y    Accept defaults and never prompt (even in a terminal).
  --no-grant   Don't add the invoking user to the docker group or chown the
               install dir — leave VAC root-only.
  --grant      Force the grant on (this is the default).
  -h, --help   Show this help and exit.

Env overrides (a pre-set value skips its question): VAC_VERSION, VAC_DOMAIN,
VAC_MANAGED_SERVICES (true/false), VAC_INSTALL_DIR, VAC_HOST_PORT, VAC_REGISTRY,
VAC_GRANT_ACCESS (1/0).
USAGE
}

show_banner() {
  # Retro VAC wordmark shown once at the top of the run. Printed only when
  # stdout is a terminal — under `curl | sh` that's true, but a redirect to a
  # file or CI log is not, so machine output stays clean.
  [ -t 1 ] || return 0
  # Wipe the screen only for a fresh interactive run where a human is watching;
  # CI / --yes / piped runs keep their scrollback. Remove this `if` block to
  # never clear the screen.
  if [ "$ASSUME_YES" != 1 ] && [ -r /dev/tty ]; then
    clear 2>/dev/null || printf '\033[H\033[2J'
  fi
  printf '%s' "$C"
  cat <<'ART'
    ██╗   ██╗ █████╗  ██████╗
    ██║   ██║██╔══██╗██╔════╝
    ██║   ██║███████║██║
    ╚██╗ ██╔╝██╔══██║██║
     ╚████╔╝ ██║  ██║╚██████╗
      ╚═══╝  ╚═╝  ╚═╝ ╚═════╝
ART
  printf '%s' "$N"
  printf '    %sself-hosted PaaS for a single box%s  ·  %s%s%s\n\n' \
    "$D" "$N" "$B" "$VAC_VERSION" "$N"
}

# ─────────────────────────────────────────────────────────────────────────────
# Small utilities
# ─────────────────────────────────────────────────────────────────────────────
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

detect_ip() {
  # Cache the best-guess public IP for the dashboard URL / DNS hints.
  [ -n "$IP" ] && { printf '%s' "$IP"; return; }
  IP="$(curl -fsS https://api.ipify.org 2>/dev/null || true)"
  [ -n "$IP" ] || IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
  [ -n "$IP" ] || IP="<server-ip>"
  printf '%s' "$IP"
}

# ─────────────────────────────────────────────────────────────────────────────
# Pre-flight & privilege
# ─────────────────────────────────────────────────────────────────────────────
handle_help() {
  # Answer --help before any preflight/elevation so it never needs Linux or
  # root. Peek without consuming "$@" so args still forward through elevation.
  for _a in "$@"; do
    case "$_a" in -h|--help) usage; exit 0 ;; esac
  done
}

preflight() {
  [ "$(uname -s)" = "Linux" ] || die "VAC installs on Linux hosts only (found $(uname -s))."
  case "$(uname -m)" in
    x86_64|amd64|aarch64|arm64) ;;
    *) die "Unsupported architecture: $(uname -m). VAC ships amd64 and arm64." ;;
  esac
}

elevate_if_needed() {
  # Re-run under sudo if we're not root. Forward "$@" so flags survive the
  # elevation: with `sh -c BODY name arg…`, name becomes $0 and the rest $1….
  [ "$(id -u)" -ne 0 ] || return 0
  command -v sudo >/dev/null 2>&1 || die "Please run as root (or install sudo)."
  info "Re-running with sudo…"
  exec sudo -E sh -c "$(cat "$0" 2>/dev/null || true)" sh "$@" 2>/dev/null \
    || die "Please run as root: curl -sSL get.vac.vojir.io | sudo sh"
}

parse_args() {
  # Parsed after self-elevation (which forwards "$@") so flags reliably cross
  # the sudo boundary. Flags win over the env defaults set above.
  while [ $# -gt 0 ]; do
    case "$1" in
      --yes|-y)   ASSUME_YES=1 ;;
      --no-grant) VAC_GRANT_ACCESS=0; GRANT_PRESET=1 ;;
      --grant)    VAC_GRANT_ACCESS=1; GRANT_PRESET=1 ;;
      -h|--help)  usage; exit 0 ;;
      *) die "unknown option: $1 (try --help)" ;;
    esac
    shift
  done
}

detect_fresh() {
  if [ -f "$ENV_FILE" ]; then FRESH=0; else FRESH=1; fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Guided setup
# ─────────────────────────────────────────────────────────────────────────────
run_wizard() {
  # Upgrades keep their config untouched — nothing to ask.
  if [ "$FRESH" != 1 ]; then
    info "Existing install found at ${B}${VAC_INSTALL_DIR}${N} — keeping secrets, upgrading images."
    return 0
  fi

  # Interactive only when there's a terminal to answer and the user didn't opt
  # out with --yes. Otherwise: announce the defaults and proceed unattended.
  if [ "$ASSUME_YES" != 1 ] && [ -r /dev/tty ]; then INTERACTIVE=1; fi
  if [ "$INTERACTIVE" != 1 ]; then
    VAC_MANAGED_SERVICES="$(normalize_bool "${VAC_MANAGED_SERVICES:-false}")"
    info "Running non-interactively — using defaults (domain: ${VAC_DOMAIN:-none}, managed services: ${VAC_MANAGED_SERVICES})."
    return 0
  fi

  wizard_welcome
  wizard_system_summary
  wizard_ask_domain
  wizard_ask_managed_services
  wizard_ask_grant
  wizard_confirm
}

wizard_welcome() {
  say ""
  say "${B}${C}Welcome to VAC${N} — a self-hosted PaaS for a single box."
  say "This will:"
  say "  • install Docker if it's missing"
  say "  • lay down the stack in ${B}${VAC_INSTALL_DIR}${N} and start it"
  say "  • install the ${B}vac${N} management command"
  say ""
  say "Nothing changes on this host until you confirm. Ctrl-C any time to abort."
}

wizard_system_summary() {
  if command -v docker >/dev/null 2>&1; then _dk="installed"; else _dk="${Y}will be installed${N}"; fi
  say ""
  say "${B}${C}System${N}"
  say "  host:    $(uname -s) $(uname -m)"
  say "  docker:  ${_dk}"
  say "  install: ${VAC_INSTALL_DIR}"
  say "  address: $(detect_ip)"
}

wizard_ask_domain() {
  [ "$DOMAIN_PRESET" = 1 ] && return 0
  say ""
  say "${B}${C}Domain${N}  (optional — you can also set this later with 'vac set-domain')"
  say "  Give VAC a domain to serve the dashboard over HTTPS and enable automatic"
  say "  per-app subdomains. Leave blank to reach it by IP for now."
  VAC_DOMAIN="$(ask 'Domain (blank = use IP):' '')"
  if [ -n "$VAC_DOMAIN" ]; then
    say "  DNS to create later:  A vac.${VAC_DOMAIN} → $(detect_ip)   and   A *.${VAC_DOMAIN} → $(detect_ip)"
  fi
}

wizard_ask_managed_services() {
  if [ "$MANAGED_PRESET" = 1 ]; then
    VAC_MANAGED_SERVICES="$(normalize_bool "$VAC_MANAGED_SERVICES")"
    return 0
  fi
  say ""
  say "${B}${C}Managed services${N}  (automatic backups, managed databases, add-on catalog)"
  say "  Off by default — it starts background workers and uses a little more RAM."
  say "  You can turn it on or off any time with: ${B}vac managed-services on|off${N}"
  if confirm 'Enable managed services now?' n; then
    VAC_MANAGED_SERVICES=true
  else
    VAC_MANAGED_SERVICES=false
  fi
}

wizard_ask_grant() {
  # Only meaningful when invoked via sudo from a real user account.
  [ "$GRANT_PRESET" = 1 ] && return 0
  _u="${SUDO_USER:-}"
  { [ -n "$_u" ] && [ "$_u" != root ]; } || return 0
  say ""
  say "${B}${C}Access${N}"
  say "  Let ${B}${_u}${N} run 'vac' without sudo? This adds them to the 'docker'"
  say "  group (root-equivalent on this host) and hands them the install dir."
  if confirm "Grant ${_u} sudo-free access?" y; then
    VAC_GRANT_ACCESS=1
  else
    VAC_GRANT_ACCESS=0
  fi
}

wizard_confirm() {
  [ "$VAC_GRANT_ACCESS" = 1 ] && _grant="yes" || _grant="no"
  say ""
  say "${B}${C}Ready to install${N}"
  say "  version:           ${VAC_VERSION}"
  say "  install dir:       ${VAC_INSTALL_DIR}"
  say "  domain:            ${VAC_DOMAIN:-none (reach by IP)}"
  say "  managed services:  ${VAC_MANAGED_SERVICES}"
  say "  sudo-free access:  ${_grant}"
  say ""
  confirm 'Proceed?' y || die "Aborted — nothing was changed."
}

# ─────────────────────────────────────────────────────────────────────────────
# Docker
# ─────────────────────────────────────────────────────────────────────────────
ensure_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    info "Docker not found — installing via get.docker.com…"
    command -v curl >/dev/null 2>&1 || die "curl is required to install Docker."
    curl -fsSL https://get.docker.com | sh
  fi

  # Ensure the daemon is up (systemd or sysvinit).
  if command -v systemctl >/dev/null 2>&1; then
    systemctl enable --now docker >/dev/null 2>&1 || true
  elif command -v service >/dev/null 2>&1; then
    service docker start >/dev/null 2>&1 || true
  fi

  docker info >/dev/null 2>&1 || die "Docker is installed but the daemon isn't reachable."
  docker compose version >/dev/null 2>&1 || die "The Docker Compose v2 plugin is required (docker compose)."
}

# ─────────────────────────────────────────────────────────────────────────────
# Install directory & assets
# ─────────────────────────────────────────────────────────────────────────────
lay_down_files() {
  info "Installing into ${B}${VAC_INSTALL_DIR}${N}"
  mkdir -p "$VAC_INSTALL_DIR"

  info "Fetching compose.prod.yaml…"
  fetch "$VAC_ASSET_BASE/compose.prod.yaml" "$COMPOSE_FILE"

  # Stash uninstall.sh next to the compose file so `vac uninstall` works offline
  # and is where an operator would look. Non-fatal — the wrapper also fetches it.
  info "Fetching uninstall.sh…"
  if fetch "$VAC_ASSET_BASE/uninstall.sh" "$VAC_INSTALL_DIR/uninstall.sh"; then
    chmod +x "$VAC_INSTALL_DIR/uninstall.sh"
  else
    warn "Could not fetch uninstall.sh; 'vac uninstall' will fall back to the network."
  fi
}

generate_env() {
  # Only on first install — secrets are preserved across re-runs.
  if [ "$FRESH" != 1 ]; then
    info "Existing config found — keeping secrets."
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
VAC_MANAGED_SERVICES=$(normalize_bool "${VAC_MANAGED_SERVICES:-false}")
EOF
    chmod 600 "$ENV_FILE"
  fi

  # Pin the requested version for this run even on upgrades.
  if grep -q '^VAC_VERSION=' "$ENV_FILE"; then
    sed -i "s|^VAC_VERSION=.*|VAC_VERSION=$VAC_VERSION|" "$ENV_FILE"
  else
    printf 'VAC_VERSION=%s\n' "$VAC_VERSION" >> "$ENV_FILE"
  fi
}

start_stack() {
  info "Pulling images (${VAC_REGISTRY}/vac-* : ${VAC_VERSION})…"
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" pull

  info "Starting VAC…"
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d
}

# ─────────────────────────────────────────────────────────────────────────────
# The `vac` management CLI
# ─────────────────────────────────────────────────────────────────────────────
install_cli() {
  info "Installing the 'vac' command…"
  write_vac_cli
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
  managed-services)
    case "${1:-}" in
      on|true|1)
        set_env VAC_MANAGED_SERVICES true; dc up -d vac-api
        echo "Managed services enabled (backups, databases, add-ons)." ;;
      off|false|0|"")
        set_env VAC_MANAGED_SERVICES false; dc up -d vac-api
        echo "Managed services disabled." ;;
      *) echo "usage: vac managed-services on|off" >&2; exit 1 ;;
    esac ;;
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
  vac managed-services on|off      toggle backups, databases & the add-on catalog
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
  # Explicit 0755 (not `chmod +x`): on a fresh install we run under `umask 077`
  # (set when writing the .env), and a who-less `+x` is masked down to owner-only
  # — leaving the wrapper root-unreadable so the granted user can't run `vac`.
  chmod 0755 /usr/local/bin/vac
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

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────
print_summary() {
  detect_ip >/dev/null

  # On a fresh install, vac-api writes a one-time setup token into its work dir.
  # Read it back through the container so we can hand the operator a ready-to-
  # click link with the token baked in — no log-digging. The token lives on a
  # named volume, so the host can't read the file directly. Upgrades have no
  # token (the admin already exists), so we only bother when FRESH=1.
  SETUP_TOKEN=""
  if [ "$FRESH" = "1" ]; then
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
    if [ -n "$VAC_DOMAIN" ]; then
      printf '    %s%shttps://vac.%s/setup?token=%s%s   %s(once DNS + TLS settle)%s\n' \
        "$B" "$G" "$VAC_DOMAIN" "$SETUP_TOKEN" "$N" "$D" "$N"
      printf '    %s%shttp://%s:%s/setup?token=%s%s   %s(direct — works right now)%s\n' \
        "$B" "$G" "$IP" "$VAC_HOST_PORT" "$SETUP_TOKEN" "$N" "$D" "$N"
    else
      printf '    %s%shttp://%s:%s/setup?token=%s%s\n' "$B" "$G" "$IP" "$VAC_HOST_PORT" "$SETUP_TOKEN" "$N"
    fi
    printf '\n  This one-time token is consumed once the account is created.\n'
  else
    printf '\n  Open the dashboard to create your admin account.\n'
  fi
  if [ "$(normalize_bool "${VAC_MANAGED_SERVICES:-false}")" = "true" ]; then
    printf '  Managed services are %son%s — backups, databases & add-ons are available.\n' "$B" "$N"
  fi
  printf '  Manage:  %svac status | vac logs | vac upgrade | vac down%s\n' "$B" "$N"
  if [ "${RELOGIN:-0}" = "1" ]; then
    printf '\n  %sLog out and back in%s (or run %snewgrp docker%s) so %s%s%s can run\n' "$B" "$N" "$B" "$N" "$B" "${SUDO_USER:-your user}" "$N"
    printf '  %svac%s commands without sudo.\n' "$B" "$N"
  fi
  printf '\n'
}

# ─────────────────────────────────────────────────────────────────────────────
# Orchestration
# ─────────────────────────────────────────────────────────────────────────────
main() {
  handle_help "$@"
  preflight
  elevate_if_needed "$@"   # re-execs as root; everything below runs privileged
  parse_args "$@"
  detect_fresh

  show_banner              # retro header (after elevation so it prints once)
  run_wizard               # gather + confirm choices before any host mutation
  ensure_docker
  lay_down_files
  generate_env
  start_stack
  install_cli
  grant_user_access
  print_summary
}

main "$@"
