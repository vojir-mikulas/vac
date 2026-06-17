import { formatBytes } from '@/lib/format'
import type { BackupConfig } from '@/types/api'

// Shared between the per-app Backups tab and the box-wide Backups overview so
// the two stay in lockstep. The labels are passed in (not resolved here) because
// each surface owns its own i18n namespace — this keeps the schedule logic in
// one place without coupling it to a single translation catalog.
export interface ScheduleLabels {
  /** "Weekly on {{day}} at {{at}} UTC" */
  weekly: (vars: { day: string; at: string }) => string
  /** "Daily at {{at}} UTC" */
  daily: (vars: { at: string }) => string
  /** Localized weekday name for index 0–6 (Sunday = 0). */
  dayName: (index: number) => string
}

export function scheduleSummary(
  c: Pick<BackupConfig, 'frequency' | 'hour_of_day' | 'day_of_week'>,
  labels: ScheduleLabels,
): string {
  const at = `${String(c.hour_of_day).padStart(2, '0')}:00`
  if (c.frequency === 'weekly' && c.day_of_week != null) {
    return labels.weekly({ day: labels.dayName(c.day_of_week), at })
  }
  return labels.daily({ at })
}

// formatBackupSize renders a run's byte count, with an em dash for "no size yet"
// (a never-run or failed config). Wraps the shared formatBytes so the nullable
// handling lives in one place.
export function formatBackupSize(n?: number | null): string {
  return n == null ? '—' : formatBytes(n)
}
