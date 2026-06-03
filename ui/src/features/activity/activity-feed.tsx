import { useState } from 'react'
import { m } from 'motion/react'
import { Bot, Eye, Undo2, User } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { EmptyState } from '@/components/common/empty-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { listItem } from '@/lib/motion'
import { useActivity, useRevertActivity, type AuditEntry } from '@/lib/api/audit'
import { relativeTime } from '@/lib/format'
import { ActivityDiffDialog } from './activity-diff-dialog'

export function ActivityFeed() {
  const { data, isLoading } = useActivity()
  const revert = useRevertActivity()
  const [preview, setPreview] = useState<AuditEntry | null>(null)
  const entries = data ?? []

  const onRevert = (e: AuditEntry) => {
    revert.mutate(e.id, {
      onSuccess: (res) => toast.success(res.summary || 'Reverted'),
      onError: (err) => toast.error(err.message),
    })
  }

  return (
    <PageContainer>
      <PageHeader
        title="Activity"
        description="Who changed what on this box. Safely-invertible changes can be reverted."
      />

      <SectionHeader>Recent activity</SectionHeader>
      <SwapFade id={isLoading ? 'loading' : entries.length === 0 ? 'empty' : 'feed'}>
        {isLoading ? (
          <ListSkeleton rows={6} avatar />
        ) : entries.length === 0 ? (
          <EmptyState
            title="No activity yet"
            description="Changes to apps, env vars, domains, and settings will appear here."
          />
        ) : (
          <Card className="gap-0 p-0">
            {entries.map((e, i) => (
              <ActivityRow
                key={e.id}
                entry={e}
                index={i}
                onRevert={() => onRevert(e)}
                onPreview={() => setPreview(e)}
                reverting={revert.isPending && revert.variables === e.id}
              />
            ))}
          </Card>
        )}
      </SwapFade>

      {preview && <ActivityDiffDialog entry={preview} onClose={() => setPreview(null)} />}
    </PageContainer>
  )
}

function ActivityRow({
  entry,
  index,
  onRevert,
  onPreview,
  reverting,
}: {
  entry: AuditEntry
  index: number
  onRevert: () => void
  onPreview: () => void
  reverting: boolean
}) {
  const failed = entry.status_code >= 400
  return (
    <m.div
      custom={index}
      variants={listItem}
      initial="hidden"
      animate="visible"
      className={`flex items-center gap-3 px-5 py-3 ${index === 0 ? '' : 'border-t'}`}
    >
      <ActorIcon type={entry.actor_type} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">
          {entry.summary || humanizeAction(entry.action)}
          {failed ? <span className="ml-2 text-2xs text-err-foreground">failed</span> : null}
        </div>
        <div className="font-mono text-2xs text-muted-foreground">
          {actorLabel(entry)} · {relativeTime(entry.created_at)}
        </div>
      </div>
      {/* Preview stays available even after an entry is reverted; Revert does not. */}
      {entry.has_preview ? (
        <Button variant="ghost" size="sm" onClick={onPreview}>
          <Eye className="size-3.5" />
          Preview
        </Button>
      ) : null}
      {entry.reverted_at ? (
        <span className="shrink-0 text-2xs text-muted-foreground">reverted</span>
      ) : entry.revertable ? (
        <Button variant="outline" size="sm" disabled={reverting} onClick={onRevert}>
          <Undo2 className="size-3.5" />
          Revert
        </Button>
      ) : null}
    </m.div>
  )
}

function ActorIcon({ type }: { type: AuditEntry['actor_type'] }) {
  const Icon = type === 'user' || type === 'api_token' ? User : Bot
  return (
    <span className="grid size-7 shrink-0 place-items-center rounded-md bg-surface-2 text-muted-foreground">
      <Icon className="size-3.5" />
    </span>
  )
}

function actorLabel(e: AuditEntry): string {
  switch (e.actor_type) {
    case 'user':
      return e.actor || 'operator'
    case 'api_token':
      return `${e.actor || 'token'} (API)`
    case 'system':
      return 'VAC (automated)'
    default:
      return 'unauthenticated'
  }
}

// humanizeAction turns "PUT /api/apps/{id}/env" into a readable fallback when a
// handler didn't supply a summary.
function humanizeAction(action: string): string {
  const [method, path = ''] = action.split(' ')
  const verb =
    method === 'POST'
      ? 'Created'
      : method === 'DELETE'
        ? 'Deleted'
        : method === 'PATCH' || method === 'PUT'
          ? 'Updated'
          : method
  const resource = path.replace(/^\/api\//, '').replace(/\{[^}]+\}/g, '…')
  return `${verb} ${resource}`
}
