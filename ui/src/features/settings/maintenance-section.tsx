import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatBytes } from '@/lib/format'
import { useDiskUsage, usePruneDisk, type DiskUsageEntry } from '@/lib/api/instance'

export function MaintenanceSection() {
  const { t } = useTranslation('settings')
  const { data, isLoading, isError } = useDiskUsage()
  const prune = usePruneDisk()

  const reclaimable = data ? data.images.reclaimable_bytes + data.build_cache.reclaimable_bytes : 0
  const nothingToReclaim = !!data && reclaimable <= 0

  const reclaim = () =>
    prune.mutate(undefined, {
      onSuccess: (r) =>
        toast.success(
          r.total_reclaimed_bytes > 0
            ? t('maintenance.reclaimed', { size: formatBytes(r.total_reclaimed_bytes) })
            : t('maintenance.reclaimedNothing'),
        ),
      onError: (e) => toast.error(e.message),
    })

  return (
    <section>
      <SectionHeader>{t('maintenance.heading')}</SectionHeader>
      <Card className="gap-5 p-5">
        <p className="text-sm text-muted-foreground">{t('maintenance.description')}</p>

        {isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : isError || !data ? (
          <p className="text-sm text-muted-foreground">{t('maintenance.loadError')}</p>
        ) : (
          <div className="flex flex-col gap-2">
            <UsageRow label={t('maintenance.images')} entry={data.images} />
            <UsageRow label={t('maintenance.buildCache')} entry={data.build_cache} />
          </div>
        )}

        <div className="flex items-center justify-between gap-4">
          <p className="text-xs text-muted-foreground">{t('maintenance.note')}</p>
          <Button
            variant="outline"
            size="sm"
            disabled={prune.isPending || nothingToReclaim}
            onClick={reclaim}
          >
            {prune.isPending ? t('maintenance.reclaiming') : t('maintenance.reclaim')}
          </Button>
        </div>
      </Card>
    </section>
  )
}

function UsageRow({ label, entry }: { label: string; entry: DiskUsageEntry }) {
  const { t } = useTranslation('settings')
  return (
    <div className="flex items-baseline justify-between gap-4 border-b pb-2 last:border-b-0 last:pb-0">
      <span className="text-sm font-medium">{label}</span>
      <span className="font-mono text-xs text-muted-foreground">
        {entry.reclaimable_bytes > 0
          ? t('maintenance.reclaimable', { size: formatBytes(entry.reclaimable_bytes) })
          : t('maintenance.nothingToReclaim')}
      </span>
    </div>
  )
}
