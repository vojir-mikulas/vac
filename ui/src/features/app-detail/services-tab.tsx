import { SectionHeader } from '@/components/common/section-header'
import { CardStackSkeleton } from '@/components/common/card-stack-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { EmptyState } from '@/components/common/empty-state'
import { ServiceCard } from '@/features/app-detail/service-card'
import { StackControls } from '@/features/app-detail/stack-controls'
import { useServices } from '@/lib/api/services'
import { useApp } from '@/lib/api/apps'
import { useBackups } from '@/lib/api/backups'
import { useInstanceInfo } from '@/lib/api/instance'

export function ServicesTab({ appId }: { appId: string }) {
  const { data: services, isLoading } = useServices(appId)
  const { data: app } = useApp(appId)
  const { data: instance } = useInstanceInfo()
  const managed = instance?.managed_services ?? false
  // Only fetch backups when the managed-services gate is open, so the warning
  // badge has data without an extra request on boxes that don't use the feature.
  const { data: backups } = useBackups(appId, managed)
  const backedUp = new Set((backups ?? []).map((b) => b.service_name))

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">Stack control</SectionHeader>
        <StackControls appId={appId} status={app?.status} />
      </div>

      <SwapFade id={isLoading ? 'loading' : services && services.length > 0 ? 'cards' : 'empty'}>
        {isLoading ? (
          <CardStackSkeleton count={2} rowHeight="h-36" />
        ) : services && services.length > 0 ? (
          <div className="flex flex-col gap-4">
            {services.map((svc) => (
              <ServiceCard
                key={svc.id}
                appId={appId}
                service={svc}
                noBackupWarning={managed && !backedUp.has(svc.name)}
              />
            ))}
          </div>
        ) : (
          <EmptyState
            title="No services detected"
            description="Services appear here after the first successful deploy."
          />
        )}
      </SwapFade>
    </div>
  )
}
