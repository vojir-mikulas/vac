import { useState } from 'react'
import { AlertTriangle, X } from 'lucide-react'

import { Sidebar } from '@/components/layout/sidebar'
import { Topbar } from '@/components/layout/topbar'
import { CommandMenu, useCommandMenu } from '@/components/layout/command-menu'

export function AppShell({ children }: { children: React.ReactNode }) {
  const [cmdOpen, setCmdOpen] = useCommandMenu()

  return (
    <div className="flex min-h-svh bg-background text-foreground">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col">
        <InsecureHTTPBanner />
        <Topbar onOpenSearch={() => setCmdOpen(true)} />
        <div className="flex-1">{children}</div>
      </main>
      <CommandMenu open={cmdOpen} onOpenChange={setCmdOpen} />
    </div>
  )
}

// One-time per browser session: hidden once the operator dismisses it; the
// next reload brings it back so the warning never goes permanently stale.
const BANNER_DISMISS_KEY = 'vac-insecure-http-dismissed'

function InsecureHTTPBanner() {
  // Decided once at mount: read window directly — this SPA never runs under
  // SSR, so the lazy initializer is safe and avoids a setState-in-effect.
  const [show, setShow] = useState(
    () =>
      window.location.protocol === 'http:' &&
      window.sessionStorage.getItem(BANNER_DISMISS_KEY) !== '1',
  )

  if (!show) return null

  return (
    <div
      role="alert"
      className="flex items-start gap-3 border-b border-warn-border bg-warn-bg px-4 py-2 text-sm text-warn-foreground md:px-6"
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
      <div className="flex-1">
        <span className="font-medium">You're on plain HTTP — sessions are insecure.</span>{' '}
        <span className="text-warn-foreground/80">
          Configure a domain with{' '}
          <code className="rounded bg-warn-foreground/10 px-1 py-0.5 font-mono text-xs">
            vac set-domain
          </code>{' '}
          to enable HTTPS.
        </span>
      </div>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => {
          window.sessionStorage.setItem(BANNER_DISMISS_KEY, '1')
          setShow(false)
        }}
        className="shrink-0 cursor-pointer rounded p-1 hover:bg-warn-foreground/10"
      >
        <X className="size-3.5" />
      </button>
    </div>
  )
}

// Standard page container — centers content at the prototype's max width.
export function PageContainer({ children }: { children: React.ReactNode }) {
  return <div className="mx-auto max-w-content px-4 pb-20 pt-7 sm:px-6 md:px-8">{children}</div>
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div className="min-w-0 flex-1 basis-72">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap gap-2">{actions}</div> : null}
    </div>
  )
}
