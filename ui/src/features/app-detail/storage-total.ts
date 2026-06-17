import type { VolumeUsage } from '@/types/api'

// sumVolumes aggregates a per-mount volume snapshot into one app total. It sums
// only measured mounts (used_bytes != null) and counts the unmeasured ones
// separately, so callers can render the total as a floor ("12 GB +1 not measured")
// rather than pretending a skipped bind-mount scan is 0 bytes. Shared by the app
// Storage card and the overview panel so the null-handling lives in one place.
export function sumVolumes(volumes: VolumeUsage[]): { total: number; unmeasured: number } {
  let total = 0
  let unmeasured = 0
  for (const v of volumes) {
    if (v.used_bytes != null) total += v.used_bytes
    else unmeasured += 1
  }
  return { total, unmeasured }
}
