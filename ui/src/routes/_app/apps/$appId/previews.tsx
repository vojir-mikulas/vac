import { createFileRoute } from '@tanstack/react-router'

import { PreviewsTab } from '@/features/app-detail/previews-tab'

export const Route = createFileRoute('/_app/apps/$appId/previews')({
  component: PreviewsRoute,
})

function PreviewsRoute() {
  const { appId } = Route.useParams()
  return <PreviewsTab appId={appId} />
}
