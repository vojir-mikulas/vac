import { createFileRoute } from '@tanstack/react-router'

import { LogExplorer } from '@/features/logs/log-explorer'

export const Route = createFileRoute('/_app/logs')({
  component: LogExplorer,
})
