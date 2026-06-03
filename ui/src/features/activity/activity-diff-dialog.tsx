import { useState } from 'react'
import { ArrowRight } from 'lucide-react'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { relativeTime } from '@/lib/format'
import { useActivityDiff, type ActivityDiff, type DiffRow, type DiffStatus } from '@/lib/api/audit'
import type { AuditEntry } from '@/lib/api/audit'

/**
 * A read-only window onto what an audit entry actually changed: a before→current
 * diff (plan 22). Secrets never reach the browser — the server masks
 * sensitive/write-only env values as `••••`. Fetched lazily when opened.
 */
export function ActivityDiffDialog({ entry, onClose }: { entry: AuditEntry; onClose: () => void }) {
  const { data, isLoading, error } = useActivityDiff(entry.id)

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Change preview</DialogTitle>
          <DialogDescription>{entry.summary || 'What this change did.'}</DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <Skeleton className="h-32 w-full rounded-lg" />
        ) : error ? (
          <p className="py-6 text-center text-sm text-err-foreground">
            {error.message || 'Could not load preview.'}
          </p>
        ) : data ? (
          <DiffRows diff={data} />
        ) : null}

        <DialogFooter className="sm:items-center sm:justify-between">
          {data ? (
            <p className="text-2xs text-muted-foreground">
              {data.changed_since ? '⚠ changed again since this action · ' : ''}Compared against
              current state ({relativeTime(data.current_as_of)})
            </p>
          ) : (
            <span />
          )}
          <Button variant="outline" size="sm" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// DiffRows is the single consistent renderer for all three diff kinds (env, app,
// base_domain). Each row is a label + before → after pair with a status pill;
// masked rows render `••••` and never the underlying value. Unchanged env rows
// collapse behind a toggle to keep large env sets readable.
export function DiffRows({ diff }: { diff: ActivityDiff }) {
  const [showUnchanged, setShowUnchanged] = useState(false)
  const unchanged = diff.rows.filter((r) => r.status === 'unchanged')
  const changed = diff.rows.filter((r) => r.status !== 'unchanged')

  if (diff.rows.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No changes recorded.</p>
  }

  const visible = showUnchanged ? diff.rows : changed

  return (
    <div className="flex flex-col gap-1">
      {visible.map((row) => (
        <DiffRowItem key={row.label} row={row} />
      ))}
      {!showUnchanged && unchanged.length > 0 ? (
        <button
          type="button"
          onClick={() => setShowUnchanged(true)}
          className="mt-1 self-start text-2xs text-muted-foreground underline-offset-2 hover:underline"
        >
          Show {unchanged.length} unchanged
        </button>
      ) : null}
      {changed.length === 0 && !showUnchanged ? (
        <p className="py-4 text-center text-sm text-muted-foreground">
          No changes to display — {unchanged.length} key(s) unchanged.
        </p>
      ) : null}
    </div>
  )
}

function DiffRowItem({ row }: { row: DiffRow }) {
  return (
    <div className="grid grid-cols-[7rem_1fr] items-center gap-3 rounded-md px-2 py-1.5 hover:bg-surface-2">
      <div className="flex min-w-0 items-center gap-2">
        <StatusPill status={row.status} />
        <span className="truncate font-mono text-xs" title={row.label}>
          {row.label}
        </span>
      </div>
      <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-2 font-mono text-xs">
        <Cell row={row} side="before" />
        <ArrowRight className="size-3 shrink-0 text-muted-foreground" />
        <Cell row={row} side="after" />
      </div>
    </div>
  )
}

// Cell renders one side of a row. The side "exists" unless this is an add
// (no before) or a remove (no after) — absent sides read as `—`. A masked value
// is shown as `••••`; the underlying value is never sent for masked rows.
function Cell({ row, side }: { row: DiffRow; side: 'before' | 'after' }) {
  const exists = side === 'before' ? row.status !== 'added' : row.status !== 'removed'
  if (!exists) return <span className="text-muted-foreground">—</span>
  const text = row.masked ? '••••' : ((side === 'before' ? row.before : row.after) ?? '••••')
  return (
    <span
      className={cn(
        'truncate rounded bg-surface-2 px-1.5 py-0.5',
        side === 'before' && row.status === 'changed' && 'text-muted-foreground line-through',
      )}
      title={row.masked ? 'hidden (sensitive)' : text}
    >
      {text || '∅'}
    </span>
  )
}

const PILL: Record<DiffStatus, { label: string; className: string }> = {
  added: { label: 'added', className: 'bg-ok-bg text-ok-foreground' },
  removed: { label: 'removed', className: 'bg-err-bg text-err-foreground' },
  changed: { label: 'changed', className: 'bg-warn-bg text-warn-foreground' },
  unchanged: { label: 'same', className: 'bg-muted text-muted-foreground' },
}

function StatusPill({ status }: { status: DiffStatus }) {
  const p = PILL[status]
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center rounded-full px-1.5 py-0.5 text-2xs font-medium',
        p.className,
      )}
    >
      {p.label}
    </span>
  )
}
