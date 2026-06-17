import { useTranslation } from 'react-i18next'
import { HardDrive } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Meter } from '@/components/common/meter'
import { SectionHeader } from '@/components/common/section-header'
import { useAppVolumes } from '@/lib/api/apps'
import { formatBytes, relativeTime } from '@/lib/format'

// StorageSection renders the latest persisted volume-usage snapshot for an app.
// It renders nothing until at least one volume has been recorded, so stateless
// apps (and apps not yet sampled) don't show an empty card. The per-app soft disk
// budget — when set — is shown as one summary fill bar against the measured total;
// per-volume rows show each mount's size and how recently it was measured (a bind
// mount that skipped its scan reads "not measured" rather than a false 0).
export function StorageSection({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data } = useAppVolumes(appId)
  const volumes = data?.volumes ?? []
  if (volumes.length === 0) return null

  // limit_bytes is the per-app budget, echoed on every row — read it once.
  const limit = volumes[0]?.limit_bytes ?? null
  const total = volumes.reduce((sum, v) => sum + (v.used_bytes ?? 0), 0)
  const pct = limit && limit > 0 ? (total / limit) * 100 : 0

  return (
    <div className="flex flex-col gap-3">
      <SectionHeader className="mb-0">{t('storage.heading')}</SectionHeader>
      <Card className="gap-4 p-5">
        {limit && limit > 0 ? (
          <div>
            <div className="mb-1.5 flex justify-between text-xs">
              <span className="text-muted-foreground">{t('storage.budget')}</span>
              <span className="font-mono tabular-nums">
                {formatBytes(total)} / {formatBytes(limit)}
              </span>
            </div>
            <Meter pct={pct} className="h-1.5" tone="brand" label={t('storage.budget')} />
          </div>
        ) : null}

        <ul className="flex flex-col divide-y">
          {volumes.map((v) => (
            <li
              key={`${v.service}-${v.mount_path}`}
              className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0"
            >
              <div className="flex min-w-0 items-center gap-2">
                <HardDrive className="size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">
                    {v.service}
                    <span className="font-normal text-muted-foreground">
                      {' · '}
                      {v.volume || v.mount_path}
                    </span>
                  </div>
                  <div className="truncate font-mono text-xs text-muted-foreground">
                    {v.mount_path}
                  </div>
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-3">
                <Badge variant="outline" className="text-[10px] uppercase">
                  {v.source === 'bind' ? t('storage.bind') : t('storage.named')}
                </Badge>
                <div className="text-right">
                  <div className="font-mono text-sm tabular-nums">
                    {v.used_bytes != null ? formatBytes(v.used_bytes) : t('storage.notMeasured')}
                  </div>
                  <div className="text-[11px] text-muted-foreground">
                    {t('storage.measured', { when: relativeTime(v.sampled_at) })}
                  </div>
                </div>
              </div>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  )
}
