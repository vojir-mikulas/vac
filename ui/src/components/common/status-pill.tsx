import { cn } from '@/lib/utils'

type Tone = 'ok' | 'warn' | 'err' | 'muted'

interface Variant {
  label: string
  tone: Tone
  pulse?: boolean
}

// Maps every backend status string (app / service / deployment) onto one of
// the four visual tones from the design.
const VARIANTS: Record<string, Variant> = {
  running: { label: 'Running', tone: 'ok' },
  degraded: { label: 'Degraded', tone: 'warn' },
  building: { label: 'Building', tone: 'warn', pulse: true },
  cloning: { label: 'Cloning', tone: 'warn', pulse: true },
  deploying: { label: 'Deploying', tone: 'warn', pulse: true },
  'health-checking': { label: 'Health check', tone: 'warn', pulse: true },
  queued: { label: 'Queued', tone: 'muted', pulse: true },
  crashed: { label: 'Crashed', tone: 'err' },
  error: { label: 'Failed', tone: 'err' },
  failed: { label: 'Failed', tone: 'err' },
  interrupted: { label: 'Interrupted', tone: 'err' },
  canceled: { label: 'Canceled', tone: 'muted' },
  stopped: { label: 'Stopped', tone: 'muted' },
  success: { label: 'Success', tone: 'ok' },
  // Scheduled-job run outcomes (plan: scheduled-jobs.md) beyond running/success/
  // failed: a run skipped because the prior one was still in flight, and a run
  // killed by its hard timeout.
  skipped: { label: 'Skipped', tone: 'muted' },
  timeout: { label: 'Timed out', tone: 'err' },
  // Domain DNS/cert states (DomainStatusState — plan 09 F3). Kept distinct so a
  // misconfigured/error domain doesn't masquerade as "Building".
  checking: { label: 'Checking', tone: 'warn', pulse: true },
  awaiting_dns: { label: 'Awaiting DNS', tone: 'warn', pulse: true },
  issuing: { label: 'Issuing cert', tone: 'warn', pulse: true },
  active: { label: 'Active', tone: 'ok' },
  misconfigured: { label: 'Misconfigured', tone: 'err' },
}

const TONE_CLASSES: Record<Tone, string> = {
  ok: 'bg-ok-bg text-ok-foreground border-ok-border',
  warn: 'bg-warn-bg text-warn-foreground border-warn-border',
  err: 'bg-err-bg text-err-foreground border-err-border',
  muted: 'bg-surface-2 text-foreground border-border',
}

const DOT_CLASSES: Record<Tone, string> = {
  ok: 'bg-ok',
  warn: 'bg-warn',
  err: 'bg-err',
  muted: 'bg-muted-foreground',
}

export function StatusPill({
  status,
  size = 'md',
  className,
}: {
  status: string
  size?: 'sm' | 'md'
  className?: string
}) {
  const v = VARIANTS[status] ?? { label: status, tone: 'muted' as const }
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border font-medium leading-tight',
        size === 'sm' ? 'px-2 py-0.5 text-2xs' : 'px-2.5 py-1 text-xs',
        TONE_CLASSES[v.tone],
        className,
      )}
    >
      <span
        className={cn('size-1.5 rounded-full', DOT_CLASSES[v.tone], v.pulse && 'animate-pulse')}
      />
      {v.label}
    </span>
  )
}
