import { createFileRoute } from '@tanstack/react-router'

import { DeploymentsSection } from '@/features/settings/deployments-section'

export const Route = createFileRoute('/_app/settings/deployments')({
  component: DeploymentsSection,
})
