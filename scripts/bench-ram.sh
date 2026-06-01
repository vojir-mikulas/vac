#!/usr/bin/env bash
#
# bench-ram.sh — defend the "<200 MB idle RAM (excl. database)" claim (plan 07).
#
# Boots a fresh vac-db + vac-api (NOT the proxy — we measure the control plane
# only, and the DB is explicitly excluded from the claim), lets the API settle,
# forces a GC via the token-gated /debug/gc endpoint, then reads the vac-api
# container's resident memory and asserts it is under the threshold.
#
#   WARN_MB  (default 180) — print a warning at/above this.
#   FAIL_MB  (default 200) — exit non-zero at/above this. This is the headline.
#   SETTLE_SECONDS (default 90) — idle time before measuring.
#
# CI runs this on a fixed-size runner so the number is comparable across PRs.
#
# NOTE ON COVERAGE: this measures the *baseline* idle control plane (no user
# apps deployed). It guards against baseline bloat — the common regression as
# subsystems accrete. It does NOT yet exercise per-app log/stats/reqmetrics/ws
# collectors under load; deploying sample apps in-harness is a follow-up (the
# collectors are subscriber-gated, so idle-with-no-apps is the honest floor).
# This gap is printed in the summary so the number isn't mistaken for "fully
# loaded".
set -euo pipefail

WARN_MB="${WARN_MB:-180}"
FAIL_MB="${FAIL_MB:-200}"
SETTLE_SECONDS="${SETTLE_SECONDS:-90}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Ephemeral secrets for the throwaway stack. Generated per run; never persisted.
export VAC_DB_PASSWORD="${VAC_DB_PASSWORD:-bench-$(openssl rand -hex 8)}"
export VAC_MASTER_KEY="${VAC_MASTER_KEY:-$(openssl rand -hex 32)}"
export VAC_METRICS_TOKEN="${VAC_METRICS_TOKEN:-$(openssl rand -hex 16)}"
# Avoid colliding with a developer's running stack on :3000.
export VAC_HOST_PORT="${VAC_HOST_PORT:-3999}"

API_URL="http://127.0.0.1:${VAC_HOST_PORT}"

log() { printf '\033[36m[bench-ram]\033[0m %s\n' "$*"; }
err() { printf '\033[31m[bench-ram]\033[0m %s\n' "$*" >&2; }

cleanup() {
  log "tearing down bench stack"
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- boot ------------------------------------------------------------------
log "building + starting vac-db and vac-api (proxy excluded; DB excluded from the number)"
docker compose up -d --build vac-db vac-api

log "waiting for /health"
for i in $(seq 1 60); do
  if curl -fsS "${API_URL}/health" >/dev/null 2>&1; then
    log "API healthy after ${i}s"
    break
  fi
  if [ "$i" -eq 60 ]; then
    err "API did not become healthy within 60s"
    docker compose logs vac-api | tail -50 >&2 || true
    exit 1
  fi
  sleep 1
done

# --- settle ----------------------------------------------------------------
log "idling ${SETTLE_SECONDS}s to reach steady state"
sleep "$SETTLE_SECONDS"

# --- force GC + read in-process heap --------------------------------------
log "forcing GC via /debug/gc"
GC_JSON="$(curl -fsS -H "Authorization: Bearer ${VAC_METRICS_TOKEN}" "${API_URL}/debug/gc" || echo '{}')"
log "post-GC heap: ${GC_JSON}"

# --- measure container RSS -------------------------------------------------
# docker stats MemUsage looks like "47.3MiB / 256MiB"; take the used side and
# normalise to MiB regardless of the unit docker chooses.
MEM_RAW="$(docker stats --no-stream --format '{{.MemUsage}}' vac-api | awk '{print $1}')"
MEM_MB="$(awk -v v="$MEM_RAW" 'BEGIN{
  u=v; sub(/[0-9.]+/,"",u); n=v; sub(/[A-Za-z]+/,"",n);
  f=1;
  if (u=="GiB") f=1024; else if (u=="MiB") f=1; else if (u=="KiB") f=1/1024;
  else if (u=="B") f=1/1048576; else if (u=="GB") f=953.674; else if (u=="kB") f=1/1024;
  printf "%.1f", n*f;
}')"

# --- report + assert -------------------------------------------------------
echo
echo "================ RAM benchmark (excl. database) ================"
printf '  vac-api RSS      : %s MiB  (raw: %s)\n' "$MEM_MB" "$MEM_RAW"
printf '  Go heap/Sys      : %s\n' "$GC_JSON"
printf '  warn threshold   : %s MiB\n' "$WARN_MB"
printf '  fail threshold   : %s MiB\n' "$FAIL_MB"
echo "  coverage         : baseline idle, no user apps (see header note)"
echo "==============================================================="
echo

# Float-safe comparisons via awk (exit code 0 == condition true).
if awk -v m="$MEM_MB" -v t="$FAIL_MB" 'BEGIN{exit !(m+0 >= t+0)}'; then
  err "FAIL: vac-api idle RSS ${MEM_MB} MiB >= ${FAIL_MB} MiB threshold"
  exit 1
fi
if awk -v m="$MEM_MB" -v t="$WARN_MB" 'BEGIN{exit !(m+0 >= t+0)}'; then
  err "WARN: vac-api idle RSS ${MEM_MB} MiB >= ${WARN_MB} MiB — trending toward the limit"
fi
log "OK: vac-api idle RSS ${MEM_MB} MiB under ${FAIL_MB} MiB"
