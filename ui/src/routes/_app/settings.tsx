import { createFileRoute } from '@tanstack/react-router'
import { Bell, Globe, KeyRound, Server, Settings2, ShieldAlert, UserCog } from 'lucide-react'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { Card } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ThemeToggle } from '@/components/theme/theme-toggle'
import { SessionsSection } from '@/features/settings/sessions-section'
import { TotpSection } from '@/features/settings/totp-section'
import { ApiTokensSection } from '@/features/settings/api-tokens-section'
import { NotificationsSection } from '@/features/settings/notifications-section'
import { DomainsSection } from '@/features/settings/domains-section'
import { InstanceSection } from '@/features/settings/instance-section'
import { DangerZoneSection } from '@/features/settings/danger-zone-section'

export const Route = createFileRoute('/_app/settings')({
  component: SettingsPage,
})

const TABS = [
  { value: 'appearance', label: 'Appearance', icon: Settings2 },
  { value: 'account', label: 'Account & security', icon: UserCog },
  { value: 'notifications', label: 'Notifications', icon: Bell },
  { value: 'tokens', label: 'API tokens', icon: KeyRound },
  { value: 'domains', label: 'Domains', icon: Globe },
  { value: 'instance', label: 'Instance', icon: Server },
  { value: 'danger', label: 'Danger zone', icon: ShieldAlert },
] as const

function SettingsPage() {
  return (
    <PageContainer>
      <PageHeader title="Settings" description="Account, security, instance, and domains." />
      <Tabs defaultValue="appearance" orientation="vertical" className="gap-6 md:flex-row">
        <TabsList
          variant="line"
          className="h-fit w-full shrink-0 gap-0.5 md:sticky md:top-3 md:w-56"
        >
          {TABS.map((t) => (
            <TabsTrigger key={t.value} value={t.value} className="justify-start gap-2">
              <t.icon className="size-4" />
              {t.label}
            </TabsTrigger>
          ))}
        </TabsList>

        <div className="min-w-0 max-w-3xl flex-1">
          <TabsContent value="appearance">
            <AppearanceSection />
          </TabsContent>
          <TabsContent value="account" className="flex flex-col gap-8">
            <TotpSection />
            <SessionsSection />
          </TabsContent>
          <TabsContent value="notifications">
            <NotificationsSection />
          </TabsContent>
          <TabsContent value="tokens">
            <ApiTokensSection />
          </TabsContent>
          <TabsContent value="domains">
            <DomainsSection />
          </TabsContent>
          <TabsContent value="instance">
            <InstanceSection />
          </TabsContent>
          <TabsContent value="danger">
            <DangerZoneSection />
          </TabsContent>
        </div>
      </Tabs>
    </PageContainer>
  )
}

function AppearanceSection() {
  return (
    <section>
      <SectionHeader>Appearance</SectionHeader>
      <Card className="p-5">
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-sm font-medium">Theme</div>
            <p className="text-xs text-muted-foreground">
              Choose light, dark, or follow your system preference.
            </p>
          </div>
          <ThemeToggle />
        </div>
      </Card>
    </section>
  )
}
