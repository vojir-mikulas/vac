import { createFileRoute } from '@tanstack/react-router'

import { JobsTab } from '@/features/app-detail/jobs-tab'

export const Route = createFileRoute('/_app/apps/$appId/jobs')({
  component: JobsRoute,
})

function JobsRoute() {
  const { appId } = Route.useParams()
  return <JobsTab appId={appId} />
}
