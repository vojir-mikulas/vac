import { createFileRoute } from '@tanstack/react-router'

import { NewApp } from '@/features/apps/new-app'

export const Route = createFileRoute('/_app/apps/new')({
  component: NewApp,
})
