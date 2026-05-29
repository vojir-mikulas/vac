import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/apps/$appId/')({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/apps/$appId/overview',
      params: { appId: params.appId },
    })
  },
})
