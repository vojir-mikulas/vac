import { createFileRoute } from '@tanstack/react-router'

import { DangerZoneSection } from '@/features/settings/danger-zone-section'

export const Route = createFileRoute('/_app/settings/danger')({
  component: DangerZoneSection,
})
