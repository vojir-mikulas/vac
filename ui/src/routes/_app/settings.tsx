import { Link, Outlet, createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
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

// `key` indexes into the `layout.tabs.*` i18n catalog; resolved at render.
const TABS = [
  { to: 'appearance', key: 'appearance', icon: Settings2 },
  { to: 'account', key: 'account', icon: UserCog },
  { to: 'notifications', key: 'notifications', icon: Bell },
  { to: 'deployments', key: 'deployments', icon: Rocket },
  { to: 'api-tokens', key: 'apiTokens', icon: KeyRound },
  { to: 'domains', key: 'domains', icon: Globe },
  { to: 'instance', key: 'instance', icon: Server },
  { to: 'danger', key: 'danger', icon: ShieldAlert },
] as const

function SettingsLayout() {
  const { t } = useTranslation('settings')
  return (
    <PageContainer>
      <PageHeader title={t('layout.title')} description={t('layout.description')} />
      <div className="flex flex-col gap-6 md:flex-row">
        <nav
          aria-label={t('layout.aria')}
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
              {t(`layout.tabs.${tab.key}`)}
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
