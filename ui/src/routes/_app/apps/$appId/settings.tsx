import { createFileRoute } from '@tanstack/react-router'

import { SettingsTab } from '@/features/app-detail/settings-tab'

export const Route = createFileRoute('/_app/apps/$appId/settings')({
  component: SettingsRoute,
})

function SettingsRoute() {
  const { appId } = Route.useParams()
  return <SettingsTab appId={appId} />
}
