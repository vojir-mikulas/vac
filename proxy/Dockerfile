# vac-proxy: stock Caddy with a bootstrap Caddyfile that opens the admin API on
# the compose network. VAC replaces the running config via the admin API on boot
# (see internal/caddy). No DNS-provider plugins are baked in — the default
# HTTP-challenge path covers the MVP; the wildcard opt-in swaps in a
# caddy-dns/<provider> build here.
FROM caddy:2-alpine

COPY Caddyfile /etc/caddy/Caddyfile

# The base image's default CMD already runs:
#   caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
