import { createFileRoute } from '@tanstack/react-router'

import { OverviewTab } from '@/features/app-detail/overview-tab'

export const Route = createFileRoute('/_app/apps/$appId/overview')({
  component: OverviewRoute,
})

function OverviewRoute() {
  const { appId } = Route.useParams()
  return <OverviewTab appId={appId} />
}
