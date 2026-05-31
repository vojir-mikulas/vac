// Small helpers shared across the mock layer. Browser-only (loaded behind the
// VITE_MOCK flag), so Date/Math are fine to use here.

let counter = 4096

export function uid(prefix: string): string {
  counter += 1
  const rand = Math.floor(Math.random() * 1e8).toString(36)
  return `${prefix}_${counter.toString(36)}${rand}`
}

export function nowISO(): string {
  return new Date().toISOString()
}

export function minutesAgoISO(min: number): string {
  return new Date(Date.now() - min * 60_000).toISOString()
}

export function daysAgoISO(days: number): string {
  return minutesAgoISO(days * 24 * 60)
}

export function randBetween(min: number, max: number): number {
  return min + Math.random() * (max - min)
}

export function randInt(min: number, max: number): number {
  return Math.floor(randBetween(min, max + 1))
}

export function pick<T>(arr: readonly T[]): T {
  // arr is always non-empty at call sites; fall back defensively for the type checker.
  return arr[Math.floor(Math.random() * arr.length)] ?? arr[0]!
}

// A short fake 40-char hex commit sha.
export function fakeSha(): string {
  let s = ''
  const hex = '0123456789abcdef'
  for (let i = 0; i < 40; i += 1) s += hex[Math.floor(Math.random() * 16)]
  return s
}

// Artificial network latency so the UI's loading states are actually exercised.
export function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
