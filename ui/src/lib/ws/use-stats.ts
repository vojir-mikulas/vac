import { useCallback, useState } from 'react'

import { useWebSocket } from '@/lib/ws/use-websocket'
import type { ServiceStatsData, WsFrame } from '@/types/api'

export interface ServiceStatsSample extends ServiceStatsData {
  service: string
  ts: string
}

export type StatsByService = Record<string, ServiceStatsSample>

// Live per-service stats for an app, keyed by service name. The newest sample
// per service replaces the previous one (the table shows current values).
export function useAppStats(appId: string, enabled = true): StatsByService {
  const [stats, setStats] = useState<StatsByService>({})

  const onFrame = useCallback((frame: WsFrame) => {
    if (frame.type !== 'stats' || !frame.service) return
    const d = frame.data as ServiceStatsData
    const service = frame.service
    setStats((prev) => ({
      ...prev,
      [service]: { ...d, service, ts: frame.ts },
    }))
  }, [])

  useWebSocket(`/api/apps/${appId}/stats`, { enabled, onFrame })
  return stats
}
