#!/bin/sh
# VAC uninstaller
#
#   curl -sSL get.vac.vojir.io/uninstall.sh | sh
#   curl -sSL get.vac.vojir.io/uninstall.sh | sh -s -- --purge --apps
#
# Tiered: defaults are safe. Each level of destruction is opt-in.
#
#   (default)        Stop the VAC stack. Keep volumes, managed apps, and
#                    the install directory. Reversible by `vac up`.
#   --purge          Also delete VAC's named volumes (DB, repos, Caddy
#                    data including ACME certs) and the install dir.
#   --apps           Also stop and delete every container, network, image
#                    and volume VAC created for managed user apps.
#   --backup DIR     Before any deletion, dump the Postgres DB and tar
#                    Caddy's data dir into DIR.
#   --keep-config    Even with --purge, preserve $VAC_INSTALL_DIR/.env so
#                    you keep your master key (for restoring from backup).
#   --yes, -y        Skip confirmation prompts.
#
# Overridable via env: VAC_INSTALL_DIR (default /opt/vac).
set -eu

# ── Config (overridable via env) ──────────────────────────────────────────--
VAC_INSTALL_DIR="${VAC_INSTALL_DIR:-/opt/vac}"
COMPOSE_FILE="$VAC_INSTALL_DIR/compose.prod.yaml"
ENV_FILE="$VAC_INSTALL_DIR/.env"
COMPOSE_PROJECT="${VAC_COMPOSE_PROJECT:-vac}"
APP_PROJECT_PREFIX="vac-"
VAC_CLI_PATH="/usr/local/bin/vac"

# ── Flags ─────────────────────────────────────────────────────────────────--
PURGE=0
APPS=0
KEEP_CONFIG=0
ASSUME_YES=0
BACKUP_DIR=""

while [ $# -gt 0 ]; do
  case "$1" in
    --purge)        PURGE=1 ;;
    --apps)         APPS=1 ;;
    --keep-config)  KEEP_CONFIG=1 ;;
    --yes|-y)       ASSUME_YES=1 ;;
    --backup)
      [ $# -ge 2 ] || { echo "--backup requires a directory" >&2; exit 1; }
      BACKUP_DIR="$2"; shift ;;
    --backup=*)     BACKUP_DIR="${1#--backup=}" ;;
    -h|--help)
      sed -n '2,/^set -eu/p' "$0" | sed 's/^# \{0,1\}//' | sed '$d'
      exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
  shift
done

# ── Output helpers ────────────────────────────────────────────────────────--
if [ -t 1 ]; then
  B="$(printf '\033[1m')"; G="$(printf '\033[32m')"; Y="$(printf '\033[33m')"
  R="$(printf '\033[31m')"; N="$(printf '\033[0m')"
else
  B=; G=; Y=; R=; N=
fi
info() { printf '%s==>%s %s\n' "$G" "$N" "$1"; }
warn() { printf '%s!  %s%s\n' "$Y" "$1" "$N"; }
die()  { printf '%serror:%s %s\n' "$R" "$N" "$1" >&2; exit 1; }

confirm() {
  # confirm "<prompt>" — returns 0 on yes, 1 on no. Auto-yes with --yes.
  [ "$ASSUME_YES" -eq 1 ] && return 0
  printf '%s%s%s [y/N] ' "$B" "$1" "$N"
  read -r reply || reply=""
  case "$reply" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

# ── Pre-flight ────────────────────────────────────────────────────────────--
# Refuse to run as a non-root user. We don't try to re-exec via sudo here
# because flag forwarding through sh's $(...) is fragile (breaks on paths
# with spaces, breaks under `curl … | sh`), and prompting for a sudo
# password mid-script obscures the destructive action — it's much clearer
# to bail with a one-line instruction.
if [ "$(id -u)" -ne 0 ]; then
  die "Please run as root, e.g. sudo $0 $*"
fi

command -v docker >/dev/null 2>&1 || die "docker not found — nothing to uninstall."
docker info >/dev/null 2>&1 || die "Docker daemon isn't reachable."

if ! docker compose version >/dev/null 2>&1; then
  warn "Docker Compose v2 plugin not available. Cleanup will fall back to raw docker commands."
  HAVE_COMPOSE=0
else
  HAVE_COMPOSE=1
fi

# ── Plan summary ──────────────────────────────────────────────────────────--
printf '\n%sVAC uninstall plan:%s\n' "$B" "$N"
printf '  install dir : %s\n' "$VAC_INSTALL_DIR"
printf '  stack       : stop + remove containers (project %s%s%s)\n' "$B" "$COMPOSE_PROJECT" "$N"
if [ "$APPS" -eq 1 ]; then
  printf '  managed apps: %sremove all VAC-deployed apps%s\n' "$Y" "$N"
else
  printf '  managed apps: leave running (use --apps to remove)\n'
fi
if [ "$PURGE" -eq 1 ]; then
  printf '  volumes     : %sDELETE DB, repos, ACME certs%s\n' "$R" "$N"
  printf '  install dir : %sDELETE %s%s\n' "$R" "$VAC_INSTALL_DIR" "$N"
  printf '  /usr/local/bin/vac: %sDELETE%s\n' "$R" "$N"
else
  printf '  volumes     : preserved (use --purge to delete)\n'
  printf '  install dir : preserved (use --purge to delete)\n'
fi
if [ -n "$BACKUP_DIR" ]; then
  printf '  backup      : %s\n' "$BACKUP_DIR"
fi
printf '\n'

if [ "$PURGE" -eq 1 ]; then
  warn "--purge deletes Caddy's /data volume, which holds your Let's Encrypt"
  warn "  certificates. Re-issuing on a new install can hit ACME rate limits."
fi
if [ "$APPS" -eq 1 ]; then
  warn "--apps will stop every container deployed through VAC. Their persistent"
  warn "  volumes will also be deleted."
fi

confirm "Proceed?" || { info "Aborted."; exit 0; }

# ── Backup (best-effort, runs before any destructive op) ─────────────────--
if [ -n "$BACKUP_DIR" ]; then
  info "Backing up to $BACKUP_DIR…"
  mkdir -p "$BACKUP_DIR"
  STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

  # Postgres dump while the DB container is still up.
  if docker ps --format '{{.Names}}' | grep -qx "${COMPOSE_PROJECT}-vac-db-1"; then
    DB_OUT="$BACKUP_DIR/vac-db-${STAMP}.sql.gz"
    info "  pg_dump → $DB_OUT"
    docker exec "${COMPOSE_PROJECT}-vac-db-1" pg_dump -U vac -d vac \
      | gzip > "$DB_OUT" \
      || warn "  pg_dump failed; see $DB_OUT for partial output"
  else
    warn "  vac-db container not running — skipping DB dump"
  fi

  # Caddy data (certs + ACME account).
  CADDY_VOL="${COMPOSE_PROJECT}_caddy_data"
  if docker volume inspect "$CADDY_VOL" >/dev/null 2>&1; then
    CADDY_OUT="$BACKUP_DIR/caddy-data-${STAMP}.tar.gz"
    info "  tar caddy_data → $CADDY_OUT"
    docker run --rm -v "$CADDY_VOL":/src:ro -v "$BACKUP_DIR":/dest alpine:3 \
      sh -c "tar -C /src -czf /dest/$(basename "$CADDY_OUT") ." \
      || warn "  caddy_data tar failed"
  else
    warn "  caddy_data volume not found — skipping"
  fi

  # Install dir (compose file + .env with master key).
  if [ -d "$VAC_INSTALL_DIR" ]; then
    DIR_OUT="$BACKUP_DIR/vac-install-${STAMP}.tar.gz"
    info "  tar install dir → $DIR_OUT"
    tar -C "$(dirname "$VAC_INSTALL_DIR")" -czf "$DIR_OUT" "$(basename "$VAC_INSTALL_DIR")" \
      || warn "  install dir tar failed"
  fi
fi

# ── Tear down managed user apps (--apps) ──────────────────────────────────--
# Done BEFORE the main stack so the proxy is still alive to drain routes.
if [ "$APPS" -eq 1 ]; then
  info "Stopping VAC-managed user apps…"
  # Find every distinct compose project starting with "vac-" (the prefix
  # VAC assigns to user stacks; see deploy/pipeline.go composeProject).
  # Excludes the VAC stack itself ("vac").
  APP_PROJECTS="$(docker ps -a --filter "label=com.docker.compose.project" --format '{{.Label "com.docker.compose.project"}}' \
    | sort -u \
    | awk -v pfx="$APP_PROJECT_PREFIX" -v self="$COMPOSE_PROJECT" \
        'index($0, pfx)==1 && $0 != self { print }')"

  if [ -z "$APP_PROJECTS" ]; then
    info "  no managed apps found."
  else
    for proj in $APP_PROJECTS; do
      info "  $proj — down --rmi local -v --remove-orphans"
      if [ "$HAVE_COMPOSE" -eq 1 ]; then
        docker compose -p "$proj" down --rmi local -v --remove-orphans \
          || warn "    docker compose down failed for $proj"
      else
        # Fallback: stop+remove containers, then rm volumes by label.
        docker ps -aq --filter "label=com.docker.compose.project=$proj" \
          | xargs -r docker rm -f >/dev/null
        docker volume ls -q --filter "label=com.docker.compose.project=$proj" \
          | xargs -r docker volume rm >/dev/null 2>&1 || true
      fi
    done
  fi
fi

# ── Tear down the VAC stack ───────────────────────────────────────────────--
info "Stopping the VAC stack…"
if [ "$HAVE_COMPOSE" -eq 1 ] && [ -f "$COMPOSE_FILE" ]; then
  DC_ARGS="-f $COMPOSE_FILE"
  [ -f "$ENV_FILE" ] && DC_ARGS="$DC_ARGS --env-file $ENV_FILE"
  if [ "$PURGE" -eq 1 ]; then
    # shellcheck disable=SC2086
    docker compose $DC_ARGS down -v --remove-orphans || warn "docker compose down failed"
  else
    # shellcheck disable=SC2086
    docker compose $DC_ARGS down --remove-orphans || warn "docker compose down failed"
  fi
else
  # Compose file missing or compose plugin unavailable — fall back to
  # removing containers by project label so we still leave a clean host.
  warn "compose file not found at $COMPOSE_FILE — falling back to label-based cleanup"
  docker ps -aq --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
    | xargs -r docker rm -f >/dev/null
  if [ "$PURGE" -eq 1 ]; then
    docker volume ls -q --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
      | xargs -r docker volume rm >/dev/null 2>&1 || true
  fi
fi

# ── Drop the edge network if nothing is still attached ────────────────────--
# vac-edge is shared between vac-proxy and every managed app, so we can only
# remove it once everything that joined it is gone (i.e. --apps was passed).
if [ "$APPS" -eq 1 ]; then
  if docker network inspect vac-edge >/dev/null 2>&1; then
    info "Removing the vac-edge network…"
    docker network rm vac-edge >/dev/null 2>&1 \
      || warn "  vac-edge still has attached containers; remove them and re-run."
  fi
fi

# ── Filesystem cleanup ────────────────────────────────────────────────────--
if [ "$PURGE" -eq 1 ]; then
  if [ -d "$VAC_INSTALL_DIR" ]; then
    if [ "$KEEP_CONFIG" -eq 1 ] && [ -f "$ENV_FILE" ]; then
      KEEP="$VAC_INSTALL_DIR/.env"
      info "Removing install dir contents (preserving $KEEP)…"
      find "$VAC_INSTALL_DIR" -mindepth 1 ! -path "$KEEP" -delete \
        || warn "  could not fully clear $VAC_INSTALL_DIR"
    else
      info "Removing $VAC_INSTALL_DIR…"
      rm -rf "$VAC_INSTALL_DIR" || warn "  rm -rf $VAC_INSTALL_DIR failed"
    fi
  fi
  if [ -f "$VAC_CLI_PATH" ]; then
    info "Removing $VAC_CLI_PATH…"
    rm -f "$VAC_CLI_PATH" || warn "  rm $VAC_CLI_PATH failed"
  fi
fi

# ── Done ──────────────────────────────────────────────────────────────────--
printf '\n%s%s VAC uninstalled.%s\n' "$B" "$G" "$N"
if [ "$PURGE" -eq 0 ]; then
  printf '\n  Volumes preserved:\n'
  docker volume ls --format '{{.Name}}' \
    | awk -v pfx="${COMPOSE_PROJECT}_" 'index($0, pfx)==1 { print "    " $0 }'
  printf '  Install dir preserved: %s\n' "$VAC_INSTALL_DIR"
  printf '\n  Run again with %s--purge%s to delete them.\n\n' "$B" "$N"
elif [ -n "$BACKUP_DIR" ]; then
  printf '\n  Backups saved to %s\n\n' "$BACKUP_DIR"
else
  printf '\n'
fi
