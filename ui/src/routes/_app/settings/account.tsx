import { createFileRoute } from '@tanstack/react-router'

import { SessionsSection } from '@/features/settings/sessions-section'
import { TotpSection } from '@/features/settings/totp-section'

export const Route = createFileRoute('/_app/settings/account')({
  component: AccountRoute,
})

function AccountRoute() {
  return (
    <div className="flex flex-col gap-8">
      <TotpSection />
      <SessionsSection />
    </div>
  )
}
