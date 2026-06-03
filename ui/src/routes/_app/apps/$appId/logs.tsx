import { createFileRoute } from '@tanstack/react-router'

import { LogsTab } from '@/features/app-detail/logs-tab'

type LogsSearch = { service?: string }

export const Route = createFileRoute('/_app/apps/$appId/logs')({
  validateSearch: (search: Record<string, unknown>): LogsSearch => ({
    service: typeof search.service === 'string' ? search.service : undefined,
  }),
  component: LogsRoute,
})

function LogsRoute() {
  const { appId } = Route.useParams()
  const { service } = Route.useSearch()
  return <LogsTab appId={appId} initialService={service} />
}
