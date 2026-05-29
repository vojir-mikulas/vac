import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Monitor } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { authApi, useSessions } from '@/lib/api/auth'
import { queryKeys } from '@/lib/query/keys'
import { relativeTime } from '@/lib/format'

export function SessionsSection() {
  const { data: sessions, isLoading } = useSessions()
  const qc = useQueryClient()

  const invalidate = () => qc.invalidateQueries({ queryKey: queryKeys.auth.sessions })

  const revoke = useMutation({
    mutationFn: (id: string) => authApi.revokeSession(id),
    onSuccess: () => {
      toast.success('Session revoked')
      invalidate()
    },
    onError: (e) => toast.error(e.message),
  })

  const revokeOthers = useMutation({
    mutationFn: () => authApi.revokeOtherSessions(),
    onSuccess: (r) => {
      toast.success(`Revoked ${r.revoked} session${r.revoked === 1 ? '' : 's'}`)
      invalidate()
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <section>
      <SectionHeader
        action={
          <Button
            variant="ghost"
            size="sm"
            disabled={revokeOthers.isPending}
            onClick={() => revokeOthers.mutate()}
          >
            Revoke all others
          </Button>
        }
      >
        Active sessions
      </SectionHeader>
      <Card className="gap-0 p-0">
        {isLoading ? (
          <div className="p-5">
            <Skeleton className="h-20 w-full" />
          </div>
        ) : (
          (sessions ?? []).map((s, i) => (
            <div
              key={s.id}
              className={`flex items-center gap-3 px-5 py-3.5 ${i > 0 ? 'border-t' : ''}`}
            >
              <Monitor className="size-4 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 text-sm">
                  <span className="truncate">{s.user_agent ?? 'Unknown device'}</span>
                  {s.is_current ? (
                    <span className="rounded bg-ok-bg px-1.5 py-0.5 text-2xs font-medium text-ok-foreground">
                      This device
                    </span>
                  ) : null}
                </div>
                <div className="font-mono text-2xs text-muted-foreground">
                  {s.ip ?? '—'} · active {relativeTime(s.last_seen_at)}
                </div>
              </div>
              {!s.is_current ? (
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={revoke.isPending}
                  onClick={() => revoke.mutate(s.id)}
                >
                  Revoke
                </Button>
              ) : null}
            </div>
          ))
        )}
      </Card>
    </section>
  )
}
