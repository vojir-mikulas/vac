import { createFileRoute } from '@tanstack/react-router'

import { AppsDashboard } from '@/features/apps/apps-dashboard'

export const Route = createFileRoute('/_app/apps/')({
  component: AppsDashboard,
})
