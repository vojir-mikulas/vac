import { SectionHeader } from '@/components/common/section-header'
import { Skeleton } from '@/components/ui/skeleton'
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

      {isLoading ? (
        <Skeleton className="h-44 w-full rounded-xl" />
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
    </div>
  )
}
