import { useEffect, useRef } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'

import { cn } from '@/lib/utils'
import { serviceColorVar } from '@/lib/service-color'
import type { LogLevel, LogLine } from '@/lib/ws/use-log-stream'

const LEVEL_CLASS: Record<LogLevel, string> = {
  info: 'text-foreground',
  ok: 'text-ok-foreground',
  warn: 'text-warn-foreground',
  error: 'text-err-foreground',
}

function timeOf(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString('en-US', { hour12: false })
}

export function LogViewer({
  lines,
  autoScroll = true,
  showService = true,
  className,
  emptyLabel = 'Waiting for logs…',
}: {
  lines: LogLine[]
  autoScroll?: boolean
  showService?: boolean
  className?: string
  emptyLabel?: string
}) {
  const parentRef = useRef<HTMLDivElement>(null)

  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 20,
    overscan: 12,
  })

  // Stick to the bottom while auto-scroll is on and new lines arrive.
  useEffect(() => {
    if (autoScroll && lines.length > 0) {
      virtualizer.scrollToIndex(lines.length - 1, { align: 'end' })
    }
  }, [autoScroll, lines.length, virtualizer])

  if (lines.length === 0) {
    return (
      <div
        className={cn(
          'grid min-h-64 place-items-center rounded-xl border bg-console font-mono text-xs text-console-muted',
          className,
        )}
      >
        {emptyLabel}
      </div>
    )
  }

  return (
    <div
      ref={parentRef}
      className={cn(
        'h-112 overflow-auto rounded-xl border bg-console p-3 font-mono text-xs leading-5',
        className,
      )}
    >
      <div style={{ height: virtualizer.getTotalSize(), position: 'relative', width: '100%' }}>
        {virtualizer.getVirtualItems().map((item) => {
          const line = lines[item.index]!
          return (
            <div
              key={line.key}
              className="absolute left-0 flex w-full gap-2 px-1 whitespace-pre-wrap"
              style={{ top: 0, transform: `translateY(${item.start}px)` }}
            >
              <span className="shrink-0 text-console-muted">{timeOf(line.ts)}</span>
              {showService && line.service ? (
                <span
                  className="shrink-0 font-medium"
                  style={{ color: serviceColorVar(line.service) }}
                >
                  {line.service}
                </span>
              ) : null}
              <span className={cn('min-w-0 flex-1', LEVEL_CLASS[line.level])}>{line.message}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
