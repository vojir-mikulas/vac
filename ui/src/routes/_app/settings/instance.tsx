import { createFileRoute } from '@tanstack/react-router'

import { InstanceSection } from '@/features/settings/instance-section'

export const Route = createFileRoute('/_app/settings/instance')({
  component: InstanceSection,
})
