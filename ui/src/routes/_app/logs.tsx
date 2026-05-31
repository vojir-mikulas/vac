import { createFileRoute, Navigate } from '@tanstack/react-router'

// TODO: Log Explorer page (post-MVP). See docs/plans/phase5-dashboard-ui.md.
export const Route = createFileRoute('/_app/logs')({
  component: () => <Navigate to="/apps" replace />,
})
