import { useMemo } from 'react'

import { LogPanel } from '@/components/common/log-panel'
import { useServices } from '@/lib/api/services'
import { useRuntimeLogs } from '@/lib/ws/use-log-stream'

export function LogsTab({ appId, initialService }: { appId: string; initialService?: string }) {
  const { data: services } = useServices(appId)
  const { lines } = useRuntimeLogs(appId)
  const serviceNames = useMemo(() => (services ?? []).map((s) => s.name), [services])

  return (
    <LogPanel
      lines={lines}
      services={serviceNames}
      initialService={initialService}
      exportName={`${appId}-logs`}
    />
  )
}
