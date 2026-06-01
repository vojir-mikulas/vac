import { createFileRoute } from '@tanstack/react-router'

import { DomainsSection } from '@/features/settings/domains-section'

export const Route = createFileRoute('/_app/settings/domains')({
  component: DomainsSection,
})
