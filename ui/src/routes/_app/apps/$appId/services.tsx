import { createFileRoute } from '@tanstack/react-router'

import { ServicesTab } from '@/features/app-detail/services-tab'

export const Route = createFileRoute('/_app/apps/$appId/services')({
  component: ServicesRoute,
})

function ServicesRoute() {
  const { appId } = Route.useParams()
  return <ServicesTab appId={appId} />
}
