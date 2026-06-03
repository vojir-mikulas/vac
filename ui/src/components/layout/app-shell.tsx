import { useState } from 'react'
import { AnimatePresence, m } from 'motion/react'
import { AlertTriangle, X } from 'lucide-react'

import { transition } from '@/lib/motion'
import { Sidebar } from '@/components/layout/sidebar'
import { Topbar } from '@/components/layout/topbar'
import { CommandMenu, useCommandMenu } from '@/components/layout/command-menu'
import { useActiveDeploymentsStream } from '@/lib/api/deployments'

export function AppShell({ children }: { children: React.ReactNode }) {
  const [cmdOpen, setCmdOpen] = useCommandMenu()
  // One deploy-queue WS connection for the whole shell: keeps the sidebar badge
  // and the Deployments page live no matter which is on screen.
  useActiveDeploymentsStream()

  return (
    <div className="flex min-h-svh bg-background text-foreground">
      <SkipToContent />
      <Sidebar />
      {/* tabIndex={-1} lets the skip link move focus here programmatically
          without adding <main> to the normal tab order. */}
      <main id="main" tabIndex={-1} className="flex min-w-0 flex-1 flex-col outline-none">
        {/* Sticky frosted region — mirrors the sidebar's floating-card inset
            (12px top/right, flush-left against the sidebar's own margin). */}
        <div className="sticky top-0 z-20 flex flex-col gap-2.5 bg-background/70 pb-2.5 pr-3 pt-3 backdrop-blur-md">
          <InsecureHTTPBanner />
          <Topbar onOpenSearch={() => setCmdOpen(true)} />
        </div>
        <div className="flex-1">{children}</div>
      </main>
      <CommandMenu open={cmdOpen} onOpenChange={setCmdOpen} />
    </div>
  )
}

// First focusable element on every page: visually hidden until focused, then it
// reveals and jumps keyboard/SR users past the sidebar straight to <main>.
function SkipToContent() {
  return (
    <a
      href="#main"
      className="sr-only focus-visible:not-sr-only focus-visible:fixed focus-visible:left-4 focus-visible:top-4 focus-visible:z-50 focus-visible:rounded-md focus-visible:border focus-visible:bg-surface-1 focus-visible:px-4 focus-visible:py-2 focus-visible:text-sm focus-visible:font-medium focus-visible:shadow-md focus-visible:ring-[3px] focus-visible:ring-ring/50"
    >
      Skip to content
    </a>
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

  // AnimatePresence with initial={false}: the banner is shown on first paint with
  // no entrance animation, but collapses (height + fade) when dismissed instead of
  // vanishing. overflow-hidden lets the height animation clip the content cleanly.
  return (
    <AnimatePresence initial={false}>
      {show ? (
        <m.div
          key="insecure-http"
          initial={{ height: 0, opacity: 0 }}
          animate={{ height: 'auto', opacity: 1 }}
          exit={{ height: 0, opacity: 0 }}
          transition={transition.base}
          className="overflow-hidden"
        >
          <div
            role="alert"
            className="flex items-center gap-3 rounded-xl border border-warn-border bg-warn-bg px-4 py-2.5 text-sm text-warn-foreground"
          >
            <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-warn-foreground/10">
              <AlertTriangle className="size-4" />
            </span>
            <div className="flex-1 leading-snug">
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
              className="-mr-1 shrink-0 cursor-pointer rounded-md p-1.5 text-warn-foreground/70 transition-colors hover:bg-warn-foreground/10 hover:text-warn-foreground"
            >
              <X className="size-3.5" />
            </button>
          </div>
        </m.div>
      ) : null}
    </AnimatePresence>
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
