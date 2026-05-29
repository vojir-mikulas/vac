import { cn } from '@/lib/utils'

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: {
  icon?: React.ComponentType<{ className?: string }>
  title: string
  description?: React.ReactNode
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'grid place-items-center rounded-xl border border-dashed bg-surface-1 px-6 py-16 text-center',
        className,
      )}
    >
      <div className="flex max-w-sm flex-col items-center gap-3">
        {Icon ? <Icon className="size-6 text-muted-foreground" /> : null}
        <div className="text-sm font-medium">{title}</div>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
        {action}
      </div>
    </div>
  )
}
