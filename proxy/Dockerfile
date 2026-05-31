# vac-proxy: stock Caddy with a bootstrap Caddyfile that opens the admin API on
# the compose network. VAC replaces the running config via the admin API on boot
# (see internal/caddy). No DNS-provider plugins are baked in — the default
# HTTP-challenge path covers the MVP; the wildcard opt-in swaps in a
# caddy-dns/<provider> build here.
FROM caddy:2-alpine

COPY Caddyfile /etc/caddy/Caddyfile

# Run with --resume so a proxy restart reloads the config VAC last pushed via
# the admin API (autosaved to /config/caddy/autosave.json, a persisted volume)
# instead of reverting to the admin-only bootstrap Caddyfile and dropping every
# route. On first boot no autosave exists yet, so Caddy falls back to --config
# and vac-api POSTs the real config (which is then autosaved). vac-api also
# self-heals a missing server tree on the next deploy as a belt-and-braces.
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile", "--resume"]
