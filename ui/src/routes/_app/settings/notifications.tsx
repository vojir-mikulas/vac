import { createFileRoute } from '@tanstack/react-router'

import { NotificationsSection } from '@/features/settings/notifications-section'

export const Route = createFileRoute('/_app/settings/notifications')({
  component: NotificationsSection,
})
