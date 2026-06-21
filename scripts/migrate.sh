#!/bin/sh
# VAC migrate — move an entire VAC install from one VPS to another.
#
#   vac migrate export [DIR]      # write a portable bundle (default ./vac-bundle-<stamp>)
#   vac migrate import <BUNDLE>   # restore a bundle onto THIS host (fresh install)
#
# This is a whole-box "snapshot" migration: it carries everything a destination
# needs to come up byte-identical — the control database, the encryption key
# (VAC_MASTER_KEY, in the .env), Caddy's ACME certificates, and every user app's
# data volume. Because the master key travels with the bundle, secrets are NOT
# re-encrypted: the destination reads them as-is.
#
# What moves:
#   • control DB        logical pg_dump of vac-db (apps, env, secrets, users, …)
#   • .env              VAC_MASTER_KEY + VAC_DB_PASSWORD + feature flags (SECRET!)
#   • caddy_data        Let's Encrypt certs + ACME account (avoids re-issue limits)
#   • vac-*  volumes    every user app's data volume + the managed-postgres volume
#
# What is intentionally NOT moved (the destination regenerates it):
#   • vac_repos         git clones — re-cloned from source on the next deploy
#   • vac_db_data       the raw PG data dir — captured logically via pg_dump instead
#   • caddy_config      Caddy runtime config — rebuilt from the DB on startup
#   • built images      rebuilt from source on the next deploy
#
# Overridable via env: VAC_INSTALL_DIR (default /opt/vac), VAC_ASSET_BASE.
set -eu

# ── Config ────────────────────────────────────────────────────────────────--
VAC_INSTALL_DIR="${VAC_INSTALL_DIR:-/opt/vac}"
COMPOSE_FILE="$VAC_INSTALL_DIR/compose.prod.yaml"
ENV_FILE="$VAC_INSTALL_DIR/.env"
COMPOSE_PROJECT="${VAC_COMPOSE_PROJECT:-vac}"
# Control-plane volumes live under the "vac_" (underscore) project prefix; user
# app stacks and the managed-postgres daemon use "vac-" (hyphen) projects, so a
# "vac-" name match cleanly selects exactly the data volumes worth carrying.
DATA_VOL_PREFIX="vac-"
# caddy_data is a control-plane volume (vac_*) but holds the ACME certs, so it is
# captured explicitly on top of the vac-* set.
CADDY_DATA_VOL="${COMPOSE_PROJECT}_caddy_data"
HELPER_IMAGE="alpine:3"

# ── Output helpers (mirror uninstall.sh) ──────────────────────────────────--
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
  [ "${ASSUME_YES:-0}" -eq 1 ] && return 0
  printf '%s%s%s [y/N] ' "$B" "$1" "$N"
  read -r reply || reply=""
  case "$reply" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

# Pin the project name explicitly: import may run from any working directory, and
# Compose's default project name is path-derived — pinning keeps it "vac" so we
# act on the real stack (and its vac_* volumes) regardless of cwd.
dc() { COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT" docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"; }

env_get() {
  # env_get FILE KEY → value (last wins), empty if absent.
  [ -f "$1" ] || return 0
  grep "^$2=" "$1" 2>/dev/null | tail -n1 | cut -d= -f2- || true
}
env_set() {
  # env_set FILE KEY VALUE — in-place upsert.
  if grep -q "^$2=" "$1" 2>/dev/null; then
    sed -i "s|^$2=.*|$2=$3|" "$1"
  else
    printf '%s=%s\n' "$2" "$3" >> "$1"
  fi
}
detect_docker_gid() {
  gid="$(getent group docker 2>/dev/null | cut -d: -f3 || true)"
  [ -n "${gid:-}" ] || gid="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 999)"
  printf '%s' "$gid"
}

# A safe-ish, low-resolution timestamp without relying on GNU date flags.
stamp() { date -u +%Y%m%dT%H%M%SZ; }

require_docker() {
  command -v docker >/dev/null 2>&1 || die "docker not found."
  docker info >/dev/null 2>&1 || die "Docker daemon isn't reachable (need root or the docker group)."
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin is required."
}

db_container() {
  # Newer installs fix the name to vac-db; older ones used the compose-generated
  # name. Echo whichever is currently running, empty if neither.
  if docker ps --format '{{.Names}}' | grep -qx "vac-db"; then
    printf 'vac-db'
  elif docker ps --format '{{.Names}}' | grep -qx "${COMPOSE_PROJECT}-vac-db-1"; then
    printf '%s-vac-db-1' "$COMPOSE_PROJECT"
  fi
}

# ── EXPORT ────────────────────────────────────────────────────────────────--
do_export() {
  DIR="${1:-}"
  [ -n "$DIR" ] || DIR="vac-bundle-$(stamp)"
  require_docker
  [ -f "$ENV_FILE" ] || die "$ENV_FILE not found — is VAC installed here?"

  DB_CTR="$(db_container)"
  [ -n "$DB_CTR" ] || die "vac-db isn't running — start the stack ('vac up') before exporting."

  mkdir -p "$DIR/volumes"
  # Resolve to an absolute path so it can be bind-mounted into helper containers.
  DIR="$(cd "$DIR" && pwd)"
  STAMP="$(stamp)"
  SRC_VERSION="$(env_get "$ENV_FILE" VAC_VERSION)"
  [ -n "$SRC_VERSION" ] || SRC_VERSION="unknown"

  warn "Tip: for crash-consistent app databases, run the export during low traffic"
  warn "  (volumes are snapshotted live; logical app data is captured as-is)."
  info "Exporting VAC → ${B}$DIR${N}"

  # 1) Control DB — logical dump (portable across PG patch versions).
  info "  pg_dump control DB → vac-db.sql.gz"
  docker exec "$DB_CTR" pg_dump -U vac -d vac | gzip > "$DIR/vac-db.sql.gz" \
    || die "pg_dump failed."

  # 2) The .env — carries VAC_MASTER_KEY (the linchpin) + VAC_DB_PASSWORD.
  info "  copy .env (contains the master key — keep the bundle secret)"
  cp "$ENV_FILE" "$DIR/env"
  chmod 600 "$DIR/env"

  # 3) Data volumes: caddy_data (certs) + every vac-* volume (apps + managed PG).
  vols="$CADDY_DATA_VOL
$(docker volume ls --format '{{.Name}}' | grep "^${DATA_VOL_PREFIX}" || true)"
  vol_json=""
  for vol in $vols; do
    [ -n "$vol" ] || continue
    if ! docker volume inspect "$vol" >/dev/null 2>&1; then
      warn "  volume $vol not found — skipping"
      continue
    fi
    info "  tar volume $vol"
    docker run --rm -v "$vol":/src:ro -v "$DIR/volumes":/dest "$HELPER_IMAGE" \
      tar -C /src -czf "/dest/$vol.tar.gz" . \
      || die "failed to archive volume $vol"
    sep=","; [ -z "$vol_json" ] && sep=""
    vol_json="$vol_json$sep
    \"$vol\""
  done

  # 4) Manifest (informational + the version guard the importer reads).
  cat > "$DIR/manifest.json" <<EOF
{
  "schema": 1,
  "tool": "vac-migrate",
  "created_at": "$STAMP",
  "source_version": "$SRC_VERSION",
  "compose_project": "$COMPOSE_PROJECT",
  "control_db": "vac-db.sql.gz",
  "env_file": "env",
  "volumes": [$vol_json
  ]
}
EOF

  printf '\n%s%s Export complete.%s\n' "$B" "$G" "$N"
  printf '  Bundle: %s\n' "$DIR"
  printf '  Size:   %s\n' "$(du -sh "$DIR" 2>/dev/null | cut -f1 || echo '?')"
  printf '\n%sThis bundle contains your master key and all secrets.%s\n' "$R" "$N"
  printf '  Transfer it over a private channel (scp/rsync over SSH), then on the new host:\n'
  printf '    %svac migrate import %s%s\n\n' "$B" "$DIR" "$N"
}

# ── IMPORT ────────────────────────────────────────────────────────────────--
do_import() {
  DIR="${1:-}"
  [ -n "$DIR" ] || die "usage: vac migrate import <BUNDLE>"
  # Accept either a bundle directory (host CLI export) or a .tar/.tar.gz file
  # (the UI 'Export instance bundle' download): extract the tarball to a temp dir
  # cleaned up on exit.
  if [ -f "$DIR" ]; then
    _extract="$(mktemp -d)"
    # shellcheck disable=SC2064
    trap "rm -rf \"$_extract\"" EXIT
    info "Extracting bundle $DIR…"
    tar -C "$_extract" -xf "$DIR" || die "could not extract bundle: $DIR"
    DIR="$_extract"
  fi
  [ -d "$DIR" ] || die "bundle not found: $DIR"
  DIR="$(cd "$DIR" && pwd)"
  [ -f "$DIR/env" ] || die "bundle is missing 'env' (the source .env)."
  [ -f "$DIR/vac-db.sql.gz" ] || die "bundle is missing 'vac-db.sql.gz'."
  [ "$(id -u)" -eq 0 ] || die "import must run as root (it rewrites $ENV_FILE and restores volumes)."
  require_docker
  [ -f "$COMPOSE_FILE" ] || die "$COMPOSE_FILE not found — run the VAC installer on this host first."

  SRC_VERSION="$(grep -o '"source_version"[^,]*' "$DIR/manifest.json" 2>/dev/null | cut -d'"' -f4 || true)"
  DST_VERSION="$(env_get "$ENV_FILE" VAC_VERSION)"

  printf '\n%sVAC import plan (destination = THIS host):%s\n' "$B" "$N"
  printf '  source version : %s\n' "${SRC_VERSION:-unknown}"
  printf '  this host      : %s\n' "${DST_VERSION:-unknown}"
  printf '  %swill overwrite:%s this host'\''s .env, control DB, and the bundled volumes\n' "$R" "$N"
  printf '  preserved      : this host'\''s DOCKER_GID (the docker socket group)\n'
  if [ -n "$SRC_VERSION" ] && [ -n "$DST_VERSION" ] && [ "$SRC_VERSION" != "$DST_VERSION" ]; then
    warn "version mismatch — the destination should run the same or a newer VAC."
    warn "  if newer, schema migrations apply on first start; a downgrade is unsafe."
  fi
  printf '\n'
  confirm "This replaces VAC state on this host. Proceed?" || { info "Aborted."; exit 0; }

  STAMP="$(stamp)"

  # 1) .env — copy the source's (so VAC_MASTER_KEY matches and secrets decrypt),
  #    but keep THIS host's DOCKER_GID: the docker group id is host-specific and
  #    a stale one locks vac-api out of the socket.
  if [ -f "$ENV_FILE" ]; then
    info "Backing up current .env → .env.pre-migrate-$STAMP"
    cp "$ENV_FILE" "$VAC_INSTALL_DIR/.env.pre-migrate-$STAMP"
  fi
  HOST_GID="$(detect_docker_gid)"
  info "Installing bundled .env (master key from source; DOCKER_GID=$HOST_GID kept local)"
  cp "$DIR/env" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  env_set "$ENV_FILE" DOCKER_GID "$HOST_GID"

  # 2) Stop the stack so nothing is mid-write while we restore.
  info "Stopping the VAC stack…"
  dc down --remove-orphans >/dev/null 2>&1 || true

  # 3) Edge network must exist before compose 'up' (it's declared external).
  docker network inspect vac-edge >/dev/null 2>&1 || {
    info "Creating the vac-edge network…"
    docker network create vac-edge >/dev/null
  }

  # 4) Restore data volumes (create if absent, wipe, then untar).
  if [ -d "$DIR/volumes" ]; then
    for arc in "$DIR"/volumes/*.tar.gz; do
      [ -e "$arc" ] || continue
      vol="$(basename "$arc" .tar.gz)"
      info "  restore volume $vol"
      docker volume create "$vol" >/dev/null
      docker run --rm -v "$vol":/dest -v "$DIR/volumes":/src:ro "$HELPER_IMAGE" \
        sh -c 'rm -rf /dest/* /dest/..?* /dest/.[!.]* 2>/dev/null; tar -C /dest -xzf "/src/'"$vol"'.tar.gz"' \
        || die "failed to restore volume $vol"
    done
  fi

  # 5) Bring up ONLY vac-db, then wipe + restore the control DB BEFORE vac-api
  #    starts — otherwise vac-api would run migrations against an empty DB.
  info "Starting vac-db…"
  dc up -d vac-db >/dev/null
  info "  waiting for Postgres…"
  i=0
  until docker exec vac-db pg_isready -U vac -d vac >/dev/null 2>&1; do
    i=$((i + 1)); [ "$i" -ge 60 ] && die "vac-db did not become ready in time."
    sleep 1
  done
  info "  resetting schema + restoring dump"
  docker exec vac-db psql -U vac -d vac -v ON_ERROR_STOP=1 \
    -c 'DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;' >/dev/null \
    || die "failed to reset the control DB schema."
  gunzip -c "$DIR/vac-db.sql.gz" \
    | docker exec -i vac-db psql -U vac -d vac -v ON_ERROR_STOP=1 -q >/dev/null \
    || die "failed to restore the control DB dump."

  # 6) Bring up the rest. vac-api reconciles desired state: it re-clones repos,
  #    rebuilds images, and brings app stacks back up — adopting the restored
  #    named volumes by name, so app data is preserved. The managed-postgres
  #    daemon (if used) starts lazily and adopts its restored volume too.
  info "Starting the full stack…"
  dc up -d >/dev/null

  printf '\n%s%s Import complete.%s\n' "$B" "$G" "$N"
  printf '  VAC will reconcile and redeploy your apps; their data volumes are restored.\n'
  printf '  Next steps:\n'
  printf '    • Point your DNS (vac.<domain> and *.<domain>) at THIS host'\''s IP.\n'
  printf '    • Watch progress:  %svac logs vac-api%s\n' "$B" "$N"
  printf '    • Old .env saved at %s/.env.pre-migrate-%s\n\n' "$VAC_INSTALL_DIR" "$STAMP"
}

# ── Dispatch ──────────────────────────────────────────────────────────────--
ASSUME_YES=0
sub="${1:-}"; [ $# -gt 0 ] && shift || true
# Each subcommand takes at most one positional (the bundle dir) plus an optional
# --yes; split them so the path stays intact even if it contains spaces.
POS=""
for a in "$@"; do
  case "$a" in
    --yes|-y) ASSUME_YES=1 ;;
    *)        POS="$a" ;;
  esac
done

case "$sub" in
  export) do_export "$POS" ;;
  import) do_import "$POS" ;;
  -h|--help|help|"")
    cat <<USAGE
vac migrate — move a whole VAC install between hosts

  vac migrate export [DIR]      write a portable bundle (default ./vac-bundle-<stamp>)
  vac migrate import <BUNDLE>   restore a bundle onto this host (replaces local state)

Options:
  --yes, -y                     skip the confirmation prompt

The bundle carries the control DB, the master key (.env), Caddy certs, and every
app data volume. It contains secrets — transfer it over a private channel only.
USAGE
    ;;
  *) die "unknown subcommand '$sub' (try: vac migrate --help)" ;;
esac
