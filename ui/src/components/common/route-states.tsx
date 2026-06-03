import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

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
