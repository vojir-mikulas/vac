import { useTranslation } from 'react-i18next'
import { ExternalLink, GitBranch, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { CardStackSkeleton } from '@/components/common/card-stack-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { SectionHeader } from '@/components/common/section-header'
import { StatusPill } from '@/components/common/status-pill'
import { usePreviewBudget, usePreviews, useTeardownPreview } from '@/lib/api/previews'
import { relativeTime } from '@/lib/format'
import type { Preview } from '@/types/api'

export function PreviewsTab({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: previews, isLoading, isError, refetch } = usePreviews(appId)
  const { data: budget } = usePreviewBudget()

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <SectionHeader className="mb-0">{t('previews.title')}</SectionHeader>
        {budget ? (
          <span className="font-mono text-xs text-muted-foreground">
            {t('previews.budget', { used: budget.used, max: budget.max })}
          </span>
        ) : null}
      </div>

      <p className="text-xs text-muted-foreground">{t('previews.intro')}</p>

      <SwapFade
        id={
          isLoading
            ? 'loading'
            : isError
              ? 'error'
              : previews && previews.length > 0
                ? 'rows'
                : 'empty'
        }
      >
        {isLoading ? (
          <CardStackSkeleton count={3} rowHeight="h-14" gap="gap-2" />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : previews && previews.length > 0 ? (
          <div className="flex flex-col gap-2">
            {previews.map((p) => (
              <PreviewRow key={p.id} appId={appId} preview={p} />
            ))}
          </div>
        ) : (
          <EmptyState
            title={t('previews.emptyTitle')}
            description={t('previews.emptyDescription')}
          />
        )}
      </SwapFade>
    </div>
  )
}

function PreviewRow({ appId, preview }: { appId: string; preview: Preview }) {
  const { t } = useTranslation('app-detail')
  const teardown = useTeardownPreview(appId)

  return (
    <Card className="flex flex-row items-center gap-4 p-0 px-5 py-3.5">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5 text-sm font-medium">
          <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate font-mono">{preview.branch}</span>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-3 font-mono text-2xs text-muted-foreground">
          {preview.url ? (
            <a
              href={preview.url}
              target="_blank"
              rel="noopener noreferrer"
              className="group flex items-center gap-1 hover:text-foreground"
            >
              <span className="group-hover:underline">
                {preview.url.replace(/^https?:\/\//, '')}
              </span>
              <ExternalLink className="size-3 opacity-0 transition-opacity group-hover:opacity-100" />
            </a>
          ) : (
            <span>{t('previews.noUrl')}</span>
          )}
          {preview.last_push_at ? (
            <span>{t('previews.lastPush', { time: relativeTime(preview.last_push_at) })}</span>
          ) : null}
        </div>
      </div>

      <StatusPill status={preview.status} size="sm" />

      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t('previews.tearDown')}
            disabled={teardown.isPending}
          >
            <Trash2 className="size-3.5" />
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('previews.tearDownDialogTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('previews.tearDownDialogDescription', { branch: preview.branch })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                teardown.mutate(preview.id, {
                  onSuccess: () => toast.success(t('previews.tornDown')),
                  onError: (e) => toast.error(e.message),
                })
              }
            >
              {t('previews.tearDown')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
