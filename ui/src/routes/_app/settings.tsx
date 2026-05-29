import { createFileRoute } from '@tanstack/react-router'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { Card } from '@/components/ui/card'
import { ThemeToggle } from '@/components/theme/theme-toggle'
import { SessionsSection } from '@/features/settings/sessions-section'
import { TotpSection } from '@/features/settings/totp-section'
import { ApiTokensSection } from '@/features/settings/api-tokens-section'
import { NotificationsSection } from '@/features/settings/notifications-section'

export const Route = createFileRoute('/_app/settings')({
  component: SettingsPage,
})

function SettingsPage() {
  return (
    <PageContainer>
      <PageHeader title="Settings" description="Account, security, and notifications." />
      <div className="flex max-w-3xl flex-col gap-8">
        <section>
          <SectionHeader>Appearance</SectionHeader>
          <Card className="p-5">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-medium">Theme</div>
                <p className="text-xs text-muted-foreground">Toggle between light and dark mode.</p>
              </div>
              <ThemeToggle />
            </div>
          </Card>
        </section>

        <TotpSection />
        <SessionsSection />
        <ApiTokensSection />
        <NotificationsSection />
      </div>
    </PageContainer>
  )
}
