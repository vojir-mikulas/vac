import { createFileRoute } from '@tanstack/react-router'

import { EnvTab } from '@/features/app-detail/env-tab'

export const Route = createFileRoute('/_app/apps/$appId/environment')({
  component: EnvRoute,
})

function EnvRoute() {
  const { appId } = Route.useParams()
  return <EnvTab appId={appId} />
}
