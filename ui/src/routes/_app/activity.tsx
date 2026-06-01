import { createFileRoute } from '@tanstack/react-router'

import { ActivityFeed } from '@/features/activity/activity-feed'

export const Route = createFileRoute('/_app/activity')({
  component: ActivityFeed,
})
