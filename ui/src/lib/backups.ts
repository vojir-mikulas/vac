import { formatBytes } from '@/lib/format'

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
  /** "Every {{minutes}} min" — only needed by surfaces with interval schedules
   *  (scheduled jobs). Optional so the backups surface, which has none, can omit it. */
  interval?: (vars: { minutes: number }) => string
}

// A schedule can be a backup config (daily/weekly) or a scheduled job, which
// adds an 'interval' frequency with interval_minutes. The Pick keeps this loose
// so both shapes satisfy it.
type Schedulable = {
  frequency: string
  hour_of_day: number
  day_of_week?: number | null
  interval_minutes?: number | null
}

export function scheduleSummary(c: Schedulable, labels: ScheduleLabels): string {
  if (c.frequency === 'interval' && c.interval_minutes != null && labels.interval) {
    return labels.interval({ minutes: c.interval_minutes })
  }
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
