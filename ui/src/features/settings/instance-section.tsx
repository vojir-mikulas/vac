import { SectionHeader } from '@/components/common/section-header'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { useInstanceInfo } from '@/lib/api/instance'

const CHANNELS = ['stable', 'beta', 'edge'] as const

export function InstanceSection() {
  const { data, isLoading } = useInstanceInfo()

  return (
    <section className="flex flex-col gap-8">
      <div>
        <SectionHeader>Version</SectionHeader>
        <Card className="gap-5 p-5">
          <Row
            label="Current"
            hint={data?.built_at ? `Built ${formatBuilt(data.built_at)}` : undefined}
          >
            {isLoading ? (
              <Skeleton className="h-5 w-24" />
            ) : (
              <span className="font-mono text-sm">vac · {data?.version || 'dev'}</span>
            )}
          </Row>

          <Row label="Update channel" hint="Switching channels is not available yet.">
            <div className="inline-flex items-center gap-0.5 rounded-md border bg-surface-1 p-0.5 opacity-60">
              {CHANNELS.map((c) => (
                <span
                  key={c}
                  className={cn(
                    'rounded px-2.5 py-1 text-xs font-medium capitalize',
                    c === (data?.channel ?? 'stable')
                      ? 'bg-surface-2 text-foreground'
                      : 'text-muted-foreground',
                  )}
                >
                  {c}
                </span>
              ))}
            </div>
          </Row>

          <Row
            label="Auto update"
            hint="Pull and self-update during the maintenance window (Sun 04:00 UTC)."
          >
            <div className="flex items-center gap-2">
              <Badge variant="secondary">Coming soon</Badge>
              <Switch checked={false} disabled aria-label="Auto update (coming soon)" />
            </div>
          </Row>
        </Card>
      </div>
    </section>
  )
}

function Row({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-sm font-medium">{label}</div>
        {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function formatBuilt(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}
