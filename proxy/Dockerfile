# vac-proxy: Caddy with the caddy-ratelimit plugin baked in, plus a bootstrap
# Caddyfile that opens the admin API on the compose network. VAC replaces the
# running config via the admin API on boot (see internal/caddy). No DNS-provider
# plugins are baked in — the default HTTP-challenge path covers the MVP; the
# wildcard opt-in adds a caddy-dns/<provider> module to the xcaddy build below.

# Build stage: compile Caddy with the rate-limit module. xcaddy resolves a
# version compatible with the builder image's Caddy (the base tag floats, so we
# let the module track it rather than hard-pinning a possibly-incompatible tag).
FROM caddy:2-builder-alpine AS builder
RUN xcaddy build \
    --with github.com/mholt/caddy-ratelimit

FROM caddy:2-alpine

# Swap the stock binary for our plugin build.
COPY --from=builder /usr/bin/caddy /usr/bin/caddy

COPY Caddyfile /etc/caddy/Caddyfile

# Run with --resume so a proxy restart reloads the config VAC last pushed via
# the admin API (autosaved to /config/caddy/autosave.json, a persisted volume)
# instead of reverting to the admin-only bootstrap Caddyfile and dropping every
# route. On first boot no autosave exists yet, so Caddy falls back to --config
# and vac-api POSTs the real config (which is then autosaved). vac-api also
# self-heals a missing server tree on the next deploy as a belt-and-braces.
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile", "--resume"]
