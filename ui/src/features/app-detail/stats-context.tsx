import { createContext, use } from 'react'

import { useAppStats, type StatsByService } from '@/lib/ws/use-stats'

const AppStatsContext = createContext<StatsByService>({})

// Subscribes once per open app-detail page and shares the live per-service
// stats map with every tab (Overview, Services) via context.
export function AppStatsProvider({
  appId,
  children,
}: {
  appId: string
  children: React.ReactNode
}) {
  const stats = useAppStats(appId)
  return <AppStatsContext value={stats}>{children}</AppStatsContext>
}

export function useAppStatsContext(): StatsByService {
  return use(AppStatsContext)
}
