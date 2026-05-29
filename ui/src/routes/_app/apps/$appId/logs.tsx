import { createFileRoute } from '@tanstack/react-router'

import { LogsTab } from '@/features/app-detail/logs-tab'

export const Route = createFileRoute('/_app/apps/$appId/logs')({
  component: LogsRoute,
})

function LogsRoute() {
  const { appId } = Route.useParams()
  return <LogsTab appId={appId} />
}
