// Deterministic per-service color slot, cycled across the 5 chart colors.
const SLOTS = 5

export function serviceColorVar(service: string | null | undefined): string {
  if (!service) return 'var(--muted-foreground)'
  let hash = 0
  for (let i = 0; i < service.length; i++) {
    hash = (hash * 31 + service.charCodeAt(i)) | 0
  }
  const slot = (Math.abs(hash) % SLOTS) + 1
  return `var(--chart-${slot})`
}
