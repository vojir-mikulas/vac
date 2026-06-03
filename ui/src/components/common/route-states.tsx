import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'

import { Button } from '@/components/ui/button'

// Shown while a route's beforeLoad/loader is in flight past defaultPendingMs, so
// a slow gate degrades to a spinner instead of freezing on the previous page.
export function RoutePendingScreen() {
  return (
    <div className="grid min-h-svh place-items-center">
      <Loader2 className="size-6 animate-spin text-muted-foreground" />
    </div>
  )
}

export function NotFoundScreen() {
  const { t } = useTranslation()
  return (
    <Screen
      title={t('notFound.title')}
      description={t('notFound.description')}
      action={
        <Button variant="brand" asChild>
          <Link to="/apps">{t('actions.backToApps')}</Link>
        </Button>
      }
    />
  )
}

export function RouteErrorScreen({ error }: { error: Error }) {
  const { t } = useTranslation()
  return (
    <Screen
      title={t('error.title')}
      description={error.message || t('error.description')}
      action={
        <Button variant="outline" onClick={() => window.location.reload()}>
          {t('actions.reload')}
        </Button>
      }
    />
  )
}

function Screen({
  title,
  description,
  action,
}: {
  title: string
  description: string
  action: React.ReactNode
}) {
  return (
    <div className="grid min-h-svh place-items-center px-4">
      <div className="flex max-w-md flex-col items-center gap-3 text-center">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        <p className="text-sm text-muted-foreground">{description}</p>
        {action}
      </div>
    </div>
  )
}
