/**
 * Most registrar DNS editors (Namecheap, GoDaddy, Cloudflare, …) take the
 * record "Host"/"Name" relative to your registered domain and append the rest
 * themselves — typing the full FQDN there produces a doubled name like
 * `app.example.com.example.com`, which is the #1 reason a record "doesn't work".
 *
 * We can't know the registered apex for certain without the public-suffix list,
 * so we assume the common case (registered domain = the last two labels) and
 * strip it. Returns "@" for the apex itself — the token most registrars use for
 * the root of the zone. A leading wildcard label is preserved (`*.test-vac`).
 */
export function relativeHost(hostname: string): string {
  const labels = hostname.split('.').filter(Boolean)
  if (labels.length <= 2) return '@'
  return labels.slice(0, labels.length - 2).join('.')
}
