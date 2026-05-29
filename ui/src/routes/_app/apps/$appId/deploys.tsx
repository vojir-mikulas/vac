import { createFileRoute } from '@tanstack/react-router'

import { DeploysTab } from '@/features/app-detail/deploys-tab'

export const Route = createFileRoute('/_app/apps/$appId/deploys')({
  component: DeploysRoute,
})

function DeploysRoute() {
  const { appId } = Route.useParams()
  return <DeploysTab appId={appId} />
}
