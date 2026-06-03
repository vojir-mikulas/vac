import { useTranslation } from 'react-i18next'
import { Check, Loader2, X } from 'lucide-react'

import { cn } from '@/lib/utils'
import { isDeployFailed, isDeploySucceeded } from '@/lib/deploy-status'
import type { DeploymentStatus } from '@/types/api'

// The pipeline stages in order. `status` maps onto the active stage.
const STEPS = [
  { key: 'queued', labelKey: 'queued' },
  { key: 'cloning', labelKey: 'cloning' },
  { key: 'building', labelKey: 'building' },
  { key: 'deploying', labelKey: 'deploying' },
  { key: 'health-checking', labelKey: 'healthChecking' },
  { key: 'success', labelKey: 'done' },
] as const

const ORDER: Record<string, number> = {
  queued: 0,
  cloning: 1,
  building: 2,
  deploying: 3,
  'health-checking': 4,
}

export function DeploySteps({ status }: { status: DeploymentStatus }) {
  const { t } = useTranslation('app-detail')
  const failed = isDeployFailed(status)
  const succeeded = isDeploySucceeded(status)
  // On success every step (including the final "Done") reads as complete;
  // on failure no step is "active" and step 0 carries the failure marker.
  const activeIndex = succeeded ? STEPS.length : (ORDER[status] ?? (failed ? -1 : 0))

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {STEPS.map((step, i) => {
        const done = !failed && i < activeIndex
        const active = !failed && i === activeIndex
        const isFailMarker = failed && i === 0
        return (
          <div key={step.key} className="flex items-center gap-1.5">
            <span
              className={cn(
                'inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-2xs font-medium',
                done && 'border-ok-border bg-ok-bg text-ok-foreground',
                active && 'border-warn-border bg-warn-bg text-warn-foreground',
                isFailMarker && 'border-err-border bg-err-bg text-err-foreground',
                !done && !active && !isFailMarker && 'border-border text-muted-foreground',
              )}
            >
              {done ? <Check className="size-3" /> : null}
              {active ? <Loader2 className="size-3 animate-spin" /> : null}
              {isFailMarker ? <X className="size-3" /> : null}
              {t(`deploySteps.${step.labelKey}`)}
            </span>
            {i < STEPS.length - 1 ? <span className="text-muted-foreground">→</span> : null}
          </div>
        )
      })}
    </div>
  )
}
