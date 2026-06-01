import { createFileRoute } from '@tanstack/react-router'

import { ApiTokensSection } from '@/features/settings/api-tokens-section'

export const Route = createFileRoute('/_app/settings/api-tokens')({
  component: ApiTokensSection,
})
