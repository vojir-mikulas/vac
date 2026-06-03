import { createContext, use } from 'react'

import { useAppStats, type StatsByService } from '@/lib/ws/use-stats'
import type { WsStatus } from '@/lib/ws/use-websocket'

interface AppStatsValue {
  stats: StatsByService
  status: WsStatus
}

const AppStatsContext = createContext<AppStatsValue>({ stats: {}, status: 'closed' })

// Subscribes once per open app-detail page and shares the live per-service
// stats map (and the stream's connection status) with every tab (Overview,
// Services) via context.
export function AppStatsProvider({
  appId,
  children,
}: {
  appId: string
  children: React.ReactNode
}) {
  const value = useAppStats(appId)
  return <AppStatsContext value={value}>{children}</AppStatsContext>
}

export function useAppStatsContext(): StatsByService {
  return use(AppStatsContext).stats
}

export function useAppStatsStatus(): WsStatus {
  return use(AppStatsContext).status
}
