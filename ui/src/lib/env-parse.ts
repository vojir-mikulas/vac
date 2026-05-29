// Parses .env-style text into a key/value map. Tolerates blank lines,
// `#` comments, optional `export ` prefixes, and single/double quoted values.
export function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
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
    out[key] = value
  }
  return out
}

const VALID_KEY = /^[A-Za-z_][A-Za-z0-9_]*$/

export function invalidEnvKeys(vars: Record<string, string>): string[] {
  return Object.keys(vars).filter((k) => !VALID_KEY.test(k))
}
