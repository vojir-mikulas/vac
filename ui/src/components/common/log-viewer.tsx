import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowDown } from 'lucide-react'
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

// Distance from the bottom (px) within which we consider the viewport "pinned"
// to the tail and keep auto-scrolling as new lines arrive.
const PIN_THRESHOLD = 24

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

  // `pinned` drives auto-scroll; `atBottom` drives the jump affordance. They
  // track the same condition but pinned is a ref so the new-lines effect reads
  // the latest value without re-subscribing.
  const pinnedRef = useRef(autoScroll)
  const [atBottom, setAtBottom] = useState(true)

  const recomputePinned = useCallback(() => {
    const el = parentRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    const isBottom = distance <= PIN_THRESHOLD
    pinnedRef.current = isBottom
    setAtBottom((prev) => (prev === isBottom ? prev : isBottom))
  }, [])

  const jumpToLatest = useCallback(() => {
    if (lines.length === 0) return
    pinnedRef.current = true
    setAtBottom(true)
    virtualizer.scrollToIndex(lines.length - 1, { align: 'end' })
  }, [lines.length, virtualizer])

  // Stick to the bottom only while pinned. A user who scrolls up unpins and is
  // left where they are until they jump back to the tail.
  useEffect(() => {
    if (autoScroll && pinnedRef.current && lines.length > 0) {
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
    <div className="relative">
      <div
        ref={parentRef}
        onScroll={recomputePinned}
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
                <span className={cn('min-w-0 flex-1', LEVEL_CLASS[line.level])}>
                  {line.message}
                </span>
              </div>
            )
          })}
        </div>
      </div>

      {!atBottom ? (
        <button
          type="button"
          onClick={jumpToLatest}
          className="absolute bottom-3 left-1/2 inline-flex -translate-x-1/2 items-center gap-1.5 rounded-full border bg-surface-2 px-3 py-1 text-2xs font-medium shadow-sm hover:bg-surface-2/70"
        >
          <ArrowDown className="size-3" />
          Jump to latest
        </button>
      ) : null}
    </div>
  )
}
