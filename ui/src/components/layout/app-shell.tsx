import { Sidebar } from '@/components/layout/sidebar'
import { Topbar } from '@/components/layout/topbar'
import { CommandMenu, useCommandMenu } from '@/components/layout/command-menu'

export function AppShell({ children }: { children: React.ReactNode }) {
  const [cmdOpen, setCmdOpen] = useCommandMenu()

  return (
    <div className="flex min-h-svh bg-background text-foreground">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col">
        <Topbar onOpenSearch={() => setCmdOpen(true)} />
        <div className="flex-1">{children}</div>
      </main>
      <CommandMenu open={cmdOpen} onOpenChange={setCmdOpen} />
    </div>
  )
}

// Standard page container — centers content at the prototype's max width.
export function PageContainer({ children }: { children: React.ReactNode }) {
  return <div className="mx-auto max-w-content px-8 pb-20 pt-7">{children}</div>
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
