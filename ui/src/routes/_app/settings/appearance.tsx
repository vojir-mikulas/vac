import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { SectionHeader } from '@/components/common/section-header'
import { LanguageSelect } from '@/components/common/language-select'
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
      <Card className="divide-y p-0">
        <div className="flex items-center justify-between gap-4 p-5">
          <div>
            <div className="text-sm font-medium">{t('appearance.theme')}</div>
            <p className="text-xs text-muted-foreground">{t('appearance.themeDescription')}</p>
          </div>
          <ThemeToggle />
        </div>
        <div className="flex items-center justify-between gap-4 p-5">
          <div>
            <div className="text-sm font-medium">{t('language.label')}</div>
            <p className="text-xs text-muted-foreground">{t('language.hint')}</p>
          </div>
          <LanguageSelect />
        </div>
      </Card>
    </section>
  )
}
