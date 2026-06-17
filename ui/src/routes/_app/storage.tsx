import { createFileRoute } from '@tanstack/react-router'

import { StoragePage } from '@/features/storage/storage-page'

export const Route = createFileRoute('/_app/storage')({
  component: StoragePage,
})
