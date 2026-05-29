import { Play, RotateCw, Square } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/common/empty-state'
import { ServiceCard } from '@/features/app-detail/service-card'
import { useServices } from '@/lib/api/services'
import { useStackControl } from '@/lib/api/apps'

export function ServicesTab({ appId }: { appId: string }) {
  const { data: services, isLoading } = useServices(appId)
  const stack = useStackControl(appId)

  const control = (action: 'start' | 'stop' | 'restart') =>
    stack.mutate(action, {
      onSuccess: () => toast.success(`Stack ${action}ed`),
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">Stack control</SectionHeader>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={stack.isPending}
            onClick={() => control('start')}
          >
            <Play className="size-3.5" />
            Start
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={stack.isPending}
            onClick={() => control('restart')}
          >
            <RotateCw className="size-3.5" />
            Restart
          </Button>
          <Button
            variant="danger"
            size="sm"
            disabled={stack.isPending}
            onClick={() => control('stop')}
          >
            <Square className="size-3.5" />
            Stop
          </Button>
        </div>
      </div>

      {isLoading ? (
        <Skeleton className="h-44 w-full rounded-xl" />
      ) : services && services.length > 0 ? (
        <div className="flex flex-col gap-4">
          {services.map((svc) => (
            <ServiceCard key={svc.id} appId={appId} service={svc} />
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
