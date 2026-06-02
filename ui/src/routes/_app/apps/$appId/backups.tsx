import { createFileRoute } from '@tanstack/react-router'

import { BackupsTab } from '@/features/app-detail/backups-tab'

export const Route = createFileRoute('/_app/apps/$appId/backups')({
  component: BackupsRoute,
})

function BackupsRoute() {
  const { appId } = Route.useParams()
  return <BackupsTab appId={appId} />
}
