// Validates a fully-qualified domain name for the domain inputs: at least two
// labels (so a bare TLD or trailing-dot is rejected), each label 1–63 chars of
// alphanumerics/hyphens without leading/trailing hyphens, a letters-only TLD,
// and ≤253 chars overall. Stricter than the old `.includes('.')` check, which
// let "a." and ".com" through. Wildcards are intentionally not accepted.
const HOSTNAME_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$/i

export function isValidHostname(host: string): boolean {
  return HOSTNAME_RE.test(host.trim())
}
