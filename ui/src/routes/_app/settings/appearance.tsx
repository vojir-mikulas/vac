import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { SectionHeader } from '@/components/common/section-header'
import { Card } from '@/components/ui/card'
import { ThemeToggle } from '@/components/theme/theme-toggle'

export const Route = createFileRoute('/_app/settings/appearance')({
  component: AppearanceSection,
})

function AppearanceSection() {
  const { t } = useTranslation('settings')
  return (
    <section>
      <SectionHeader>{t('appearance.title')}</SectionHeader>
      <Card className="p-5">
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-sm font-medium">{t('appearance.theme')}</div>
            <p className="text-xs text-muted-foreground">{t('appearance.themeDescription')}</p>
          </div>
          <ThemeToggle />
        </div>
      </Card>
    </section>
  )
}
