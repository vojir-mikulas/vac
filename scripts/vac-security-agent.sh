#!/bin/sh
# vac-security-agent — host-side, read-only security collector for VAC.
#
# VAC's control plane (vac-api) runs in a deliberately sandboxed container: it has
# no fail2ban-client / ufw / nft on PATH and no privilege to read host firewall
# state. So this tiny agent runs ON THE HOST (installed by the VAC installer as a
# systemd timer, ~every 60s) and writes a snapshot of fail2ban + firewall state
# into a file that vac-api bind-mounts read-only. The control plane never gains
# privilege; the host pushes state in.
#
# It is strictly read-only: only `status` / list subcommands are ever invoked.
#
# Output: $VAC_SECURITY_DIR/host.snapshot (default /var/lib/vac/security), written
# atomically (temp + mv). Format is a "generated_at:" preamble line followed by
# "@@@ <section>" markers whose bodies are the verbatim command outputs:
#   @@@ fail2ban-status        # presence marker (fail2ban-client status)
#   @@@ fail2ban-jail <name>   # one per jail
#   @@@ firewall ufw|nftables|none
set -eu

DIR="${VAC_SECURITY_DIR:-/var/lib/vac/security}"
OUT="$DIR/host.snapshot"
PATH="/usr/sbin:/usr/bin:/sbin:/bin:$PATH"

mkdir -p "$DIR"
TMP="$(mktemp "$DIR/.host.snapshot.XXXXXX")"
# Best-effort cleanup if we exit before the atomic rename.
trap 'rm -f "$TMP"' EXIT

# RFC3339 UTC timestamp; vac-api uses it to flag a stale (timer-stopped) snapshot.
printf 'generated_at: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$TMP"

# ── fail2ban ─────────────────────────────────────────────────────────────────
# Only emit sections when fail2ban-client is present AND the daemon answers; a
# missing/unreadable fail2ban yields no sections → vac-api reports "not detected".
if command -v fail2ban-client >/dev/null 2>&1; then
  if status="$(fail2ban-client status 2>/dev/null)"; then
    printf '@@@ fail2ban-status\n%s\n' "$status" >> "$TMP"
    # The "Jail list:" line is a comma-separated list; split it into names.
    jails="$(printf '%s\n' "$status" | sed -n 's/.*Jail list:[[:space:]]*//p' | tr ',' ' ')"
    for jail in $jails; do
      [ -n "$jail" ] || continue
      if jout="$(fail2ban-client status "$jail" 2>/dev/null)"; then
        printf '@@@ fail2ban-jail %s\n%s\n' "$jail" "$jout" >> "$TMP"
      fi
    done
  fi
fi

# ── firewall ─────────────────────────────────────────────────────────────────
# Prefer ufw, then nftables, matching vac-api's exec fallback. "none" is explicit
# so the dashboard can distinguish "checked, nothing found" from "agent absent".
if command -v ufw >/dev/null 2>&1 && fw="$(ufw status verbose 2>/dev/null)"; then
  printf '@@@ firewall ufw\n%s\n' "$fw" >> "$TMP"
elif command -v nft >/dev/null 2>&1 && fw="$(nft list ruleset 2>/dev/null)"; then
  printf '@@@ firewall nftables\n%s\n' "$fw" >> "$TMP"
else
  printf '@@@ firewall none\n' >> "$TMP"
fi

# Atomic publish + world-readable so the (non-root) vac container user can read it.
chmod 0644 "$TMP"
mv -f "$TMP" "$OUT"
trap - EXIT
