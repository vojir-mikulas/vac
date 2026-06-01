import { createFileRoute } from '@tanstack/react-router'

import { SectionHeader } from '@/components/common/section-header'
import { Card } from '@/components/ui/card'
import { ThemeToggle } from '@/components/theme/theme-toggle'

export const Route = createFileRoute('/_app/settings/appearance')({
  component: AppearanceSection,
})

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
