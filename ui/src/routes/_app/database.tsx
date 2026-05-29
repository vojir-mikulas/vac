import { createFileRoute } from '@tanstack/react-router'

import { DatabasePage } from '@/features/database/database-page'

export const Route = createFileRoute('/_app/database')({
  component: DatabasePage,
})
