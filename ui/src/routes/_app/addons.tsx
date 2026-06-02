import { createFileRoute } from '@tanstack/react-router'

import { AddonsPage } from '@/features/addons/addons-page'

export const Route = createFileRoute('/_app/addons')({
  component: AddonsPage,
})
