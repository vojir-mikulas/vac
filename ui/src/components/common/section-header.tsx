import { cn } from '@/lib/utils'

export function SectionHeader({
  children,
  action,
  className,
}: {
  children: React.ReactNode
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('mb-2.5 flex items-center justify-between pl-0.5', className)}>
      <h3 className="text-2xs font-medium uppercase tracking-wider text-muted-foreground">
        {children}
      </h3>
      {action}
    </div>
  )
}
