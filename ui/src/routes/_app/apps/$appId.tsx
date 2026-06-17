import { Link, Outlet, createFileRoute } from '@tanstack/react-router'
import { Blocks, ExternalLink, GitBranch, Globe, Lock, RotateCw } from 'lucide-react'

import { PageContainer } from '@/components/layout/app-shell'
import { BrandIcon, brandFor } from '@/components/common/brand-icon'
import { AppStatsProvider } from '@/features/app-detail/stats-context'
import { LiveDeployBanner } from '@/features/app-detail/live-deploy-banner'
import { StackControls } from '@/features/app-detail/stack-controls'
import { StatusPill } from '@/components/common/status-pill'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { useApp } from '@/lib/api/apps'
import { useDatabases } from '@/lib/api/databases'
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
  { to: 'jobs', label: 'Jobs' },
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
  const { data: databases } = useDatabases(appId, !!instance?.managed_services)
  const deploy = useTriggerDeploy(appId)

  const primaryDomain = domains?.[0]
  const isAddon = app?.source === 'template'
  const hasManagedDB = (databases?.length ?? 0) > 0

  // Slot the managed-services tabs (Databases/Backups) in before Settings when
  // the gate is open — but hide them for an add-on app that owns no managed DB:
  // they'd only show empty inputs for a stack the operator doesn't manage here.
  const showManagedTabs = !!instance?.managed_services && (!isAddon || hasManagedDB)

  // Assemble the tab strip: base tabs, a Previews tab after Deploys (only on a
  // real parent app — a preview has no previews of its own), and the managed
  // tabs before Settings. `to` stays a literal union so the Link route type holds.
  type Tab = {
    to:
      | 'overview'
      | 'services'
      | 'deploys'
      | 'previews'
      | 'logs'
      | 'jobs'
      | 'environment'
      | 'settings'
      | 'backups'
      | 'databases'
    label: string
  }
  const tabs: Tab[] = [...TABS]
  if (app && !app.is_preview) {
    tabs.splice(tabs.findIndex((tb) => tb.to === 'deploys') + 1, 0, {
      to: 'previews',
      label: 'Previews',
    })
  }
  if (showManagedTabs) {
    tabs.splice(
      tabs.findIndex((tb) => tb.to === 'settings'),
      0,
      ...MANAGED_TABS,
    )
  }

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
              {app.is_preview ? (
                <span className="rounded-sm bg-brand/10 px-1.5 py-0.5 text-2xs font-medium text-brand">
                  Preview
                </span>
              ) : null}
            </div>
          )}
          {app ? (
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-muted-foreground">
              {isAddon ? (
                <span className="flex items-center gap-1.5">
                  {brandFor(app.template_icon) ? (
                    <BrandIcon brand={app.template_icon} className="size-3" />
                  ) : (
                    <Blocks className="size-3" />
                  )}
                  Installed from {app.template_name ?? 'add-on'}
                </span>
              ) : (
                <span className="flex items-center gap-1.5">
                  <GitBranch className="size-3" />
                  {app.git_url} : {app.git_branch}
                </span>
              )}
              {primaryDomain ? (
                <a
                  href={`https://${primaryDomain.hostname}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="group flex items-center gap-1.5 hover:text-foreground"
                >
                  {primaryDomain.status === 'active' ? (
                    <Lock className="size-3 text-ok" />
                  ) : (
                    <Globe className="size-3" />
                  )}
                  <span className="group-hover:underline">{primaryDomain.hostname}</span>
                  <ExternalLink className="size-3 opacity-0 transition-opacity group-hover:opacity-100" />
                </a>
              ) : null}
            </div>
          ) : null}
        </div>

        <div className="flex items-center gap-3">
          {app ? <StackControls appId={appId} status={app.status} compact /> : null}
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
      </div>

      <LiveDeployBanner appId={appId} />

      <ScrollArea className="mb-6">
        <nav aria-label="App sections" className="flex gap-1 border-b">
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
      </ScrollArea>

      <AppStatsProvider appId={appId}>
        <Outlet />
      </AppStatsProvider>
    </PageContainer>
  )
}
