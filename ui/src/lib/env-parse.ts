// Parses .env-style text into a key/value map. Tolerates blank lines,
// `#` comments, optional `export ` prefixes, and single/double quoted values.
export function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const { key, value } of parseEnvEntries(text)) {
    out[key] = value
  }
  return out
}

export interface ParsedEnvEntry {
  key: string
  value: string
}

// parseEnvEntries is the order-preserving form of parseEnv — later duplicate
// keys overwrite earlier ones (last wins), matching dotenv semantics, while the
// first-seen position is kept.
export function parseEnvEntries(text: string): ParsedEnvEntry[] {
  const out: ParsedEnvEntry[] = []
  const index = new Map<string, number>()
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const withoutExport = line.startsWith('export ') ? line.slice('export '.length) : line
    const eq = withoutExport.indexOf('=')
    if (eq === -1) continue
    const key = withoutExport.slice(0, eq).trim()
    if (!key) continue
    let value = withoutExport.slice(eq + 1).trim()
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1)
    }
    const at = index.get(key)
    const existing = at === undefined ? undefined : out[at]
    if (existing) {
      existing.value = value
    } else {
      index.set(key, out.length)
      out.push({ key, value })
    }
  }
  return out
}

const VALID_KEY = /^[A-Za-z_][A-Za-z0-9_]*$/

export function invalidEnvKeys(vars: Record<string, string>): string[] {
  return Object.keys(vars).filter((k) => !VALID_KEY.test(k))
}

export function isValidEnvKey(key: string): boolean {
  return VALID_KEY.test(key)
}

// Heuristic for the opt-in "auto-mark secrets" import toggle: a key whose name
// looks like it holds a credential is flagged sensitive. Case-insensitive
// substring match — deliberately broad; the user can flip any row afterward.
const SENSITIVE_KEY_RE =
  /SECRET|TOKEN|KEY|PASSWORD|PASSWD|PASS|PRIVATE|CREDENTIAL|AUTH|DSN|CONNECTION/i

export function isSensitiveKey(key: string): boolean {
  return SENSITIVE_KEY_RE.test(key)
}
