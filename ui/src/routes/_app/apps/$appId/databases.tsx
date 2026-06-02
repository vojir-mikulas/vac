import { createFileRoute } from '@tanstack/react-router'

import { DatabasesTab } from '@/features/app-detail/databases-tab'

export const Route = createFileRoute('/_app/apps/$appId/databases')({
  component: DatabasesRoute,
})

function DatabasesRoute() {
  const { appId } = Route.useParams()
  return <DatabasesTab appId={appId} />
}
