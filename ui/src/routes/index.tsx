import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/')({
  component: HomePage,
})

function HomePage() {
  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <div className="text-center">
        <h1 className="text-4xl font-semibold tracking-tight">VAC</h1>
        <p className="text-muted-foreground mt-2">Dashboard scaffold — ready to build.</p>
      </div>
    </div>
  )
}
