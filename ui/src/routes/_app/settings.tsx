import { Link, Outlet, createFileRoute } from '@tanstack/react-router'
import {
  Bell,
  Globe,
  KeyRound,
  Rocket,
  Server,
  Settings2,
  ShieldAlert,
  UserCog,
} from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'

export const Route = createFileRoute('/_app/settings')({
  component: SettingsLayout,
})

const TABS = [
  { to: 'appearance', label: 'Appearance', icon: Settings2 },
  { to: 'account', label: 'Account & security', icon: UserCog },
  { to: 'notifications', label: 'Notifications', icon: Bell },
  { to: 'deployments', label: 'Deployments', icon: Rocket },
  { to: 'api-tokens', label: 'API tokens', icon: KeyRound },
  { to: 'domains', label: 'Domains', icon: Globe },
  { to: 'instance', label: 'Instance', icon: Server },
  { to: 'danger', label: 'Danger zone', icon: ShieldAlert },
] as const

function SettingsLayout() {
  return (
    <PageContainer>
      <PageHeader title="Settings" description="Account, security, instance, and domains." />
      <div className="flex flex-col gap-6 md:flex-row">
        <nav
          aria-label="Settings sections"
          className="flex h-fit w-full shrink-0 flex-col gap-0.5 md:sticky md:top-3 md:w-56"
        >
          {TABS.map((tab) => (
            <Link
              key={tab.to}
              to={`/settings/${tab.to}`}
              className="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground data-[status=active]:bg-muted data-[status=active]:text-foreground"
              activeProps={{ 'data-status': 'active', 'aria-current': 'page' }}
            >
              <tab.icon className="size-4" />
              {tab.label}
            </Link>
          ))}
        </nav>

        <div className="min-w-0 max-w-3xl flex-1">
          <Outlet />
        </div>
      </div>
    </PageContainer>
  )
}
