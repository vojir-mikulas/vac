import { Link, Outlet, createFileRoute } from '@tanstack/react-router'
import { GitBranch, Globe, Lock, RotateCw } from 'lucide-react'

import { PageContainer } from '@/components/layout/app-shell'
import { AppStatsProvider } from '@/features/app-detail/stats-context'
import { LiveDeployBanner } from '@/features/app-detail/live-deploy-banner'
import { StatusPill } from '@/components/common/status-pill'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useApp } from '@/lib/api/apps'
import { useTriggerDeploy } from '@/lib/api/deployments'
import { useDomains } from '@/lib/api/domains'
import { useInstanceInfo } from '@/lib/api/instance'
import { toast } from 'sonner'

export const Route = createFileRoute('/_app/apps/$appId')({
  component: AppDetailLayout,
})

const TABS = [
  { to: 'overview', label: 'Overview' },
  { to: 'services', label: 'Services' },
  { to: 'deploys', label: 'Deploys' },
  { to: 'logs', label: 'Logs' },
  { to: 'environment', label: 'Environment' },
  { to: 'settings', label: 'Settings' },
] as const

// Tabs shown only when the managed-services gate (Track D) is open.
const MANAGED_TABS = [
  { to: 'backups', label: 'Backups' },
  { to: 'databases', label: 'Databases' },
] as const

function AppDetailLayout() {
  const { appId } = Route.useParams()
  const { data: app, isLoading } = useApp(appId)
  const { data: domains } = useDomains(appId)
  const { data: instance } = useInstanceInfo()
  const deploy = useTriggerDeploy(appId)

  const primaryDomain = domains?.[0]

  // Slot the managed-services tabs in before Settings when the gate is open.
  const tabs = instance?.managed_services
    ? [...TABS.slice(0, 5), ...MANAGED_TABS, ...TABS.slice(5)]
    : TABS

  return (
    <PageContainer>
      <div className="mb-5 flex flex-wrap items-start justify-between gap-6">
        <div className="min-w-0">
          {isLoading || !app ? (
            <Skeleton className="h-8 w-48" />
          ) : (
            <div className="flex items-center gap-3">
              <h1 className="truncate text-2xl font-semibold tracking-tight">{app.name}</h1>
              <StatusPill status={app.status} />
            </div>
          )}
          {app ? (
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5">
                <GitBranch className="size-3" />
                {app.git_url} : {app.git_branch}
              </span>
              {primaryDomain ? (
                <span className="flex items-center gap-1.5">
                  {primaryDomain.status === 'active' ? (
                    <Lock className="size-3 text-ok" />
                  ) : (
                    <Globe className="size-3" />
                  )}
                  {primaryDomain.hostname}
                </span>
              ) : null}
            </div>
          ) : null}
        </div>

        <Button
          variant="brand"
          disabled={deploy.isPending}
          onClick={() =>
            deploy.mutate(undefined, {
              onSuccess: () => toast.success('Deploy triggered'),
              onError: (e) => toast.error(e.message),
            })
          }
        >
          <RotateCw className="size-4" />
          Deploy from HEAD
        </Button>
      </div>

      <LiveDeployBanner appId={appId} />

      <nav aria-label="App sections" className="mb-6 flex gap-1 overflow-x-auto border-b">
        {tabs.map((tab) => (
          <Link
            key={tab.to}
            to={`/apps/$appId/${tab.to}`}
            params={{ appId }}
            className="-mb-px shrink-0 border-b-2 border-transparent px-3 py-2.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground data-[status=active]:border-foreground data-[status=active]:text-foreground"
            activeProps={{ 'data-status': 'active', 'aria-current': 'page' }}
          >
            {tab.label}
          </Link>
        ))}
      </nav>

      <AppStatsProvider appId={appId}>
        <Outlet />
      </AppStatsProvider>
    </PageContainer>
  )
}
