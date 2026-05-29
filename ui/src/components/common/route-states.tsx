import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'

export function NotFoundScreen() {
  return (
    <Screen
      title="Page not found"
      description="The page you're looking for doesn't exist."
      action={
        <Button variant="brand" asChild>
          <Link to="/apps">Back to apps</Link>
        </Button>
      }
    />
  )
}

export function RouteErrorScreen({ error }: { error: Error }) {
  return (
    <Screen
      title="Something went wrong"
      description={error.message || 'An unexpected error occurred.'}
      action={
        <Button variant="outline" onClick={() => window.location.reload()}>
          Reload
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
