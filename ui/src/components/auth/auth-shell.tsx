import { m } from 'motion/react'

import { AuthBackground } from './auth-background'

export function AuthShell({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <div className="relative flex min-h-svh items-center justify-center overflow-hidden bg-surface-1 px-4 py-12">
      <AuthBackground />
      <m.div
        className="relative z-10 w-full max-w-sm"
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: [0.22, 1, 0.36, 1] }}
      >
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <img src="/vac-logo.svg" alt="VAC" className="size-10 rounded-lg" />
          <div>
            <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
            {description ? (
              <p className="mt-1 text-sm text-muted-foreground">{description}</p>
            ) : null}
          </div>
        </div>
        {children}
      </m.div>
    </div>
  )
}
