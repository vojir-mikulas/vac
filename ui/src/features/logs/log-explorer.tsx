import { useEffect, useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { ErrorState } from '@/components/common/error-state'
import { LogViewer } from '@/components/common/log-viewer'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useApps } from '@/lib/api/apps'
import { useServices } from '@/lib/api/services'
import { useLogSearch, type LogSearchFilters } from '@/lib/api/logs'
import { levelFor, type LogLine } from '@/lib/ws/use-log-stream'

const STREAMS = ['all', 'stdout', 'stderr', 'system'] as const

const ALL = '__all__' // Select needs a non-empty value for the "any" option.

export function LogExplorer() {
  const { t } = useTranslation('logs')

  const [app, setApp] = useState(ALL)
  const [service, setService] = useState(ALL)
  const [stream, setStream] = useState('all')
  const [searchInput, setSearchInput] = useState('')
  const [query, setQuery] = useState('')

  // Debounce the free-text input so each keystroke doesn't fire a request.
  useEffect(() => {
    const id = setTimeout(() => setQuery(searchInput.trim()), 300)
    return () => clearTimeout(id)
  }, [searchInput])

  const appsQuery = useApps()
  const apps = useMemo(() => appsQuery.data ?? [], [appsQuery.data])
  const appNames = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps])

  // Service options are only meaningful once a single app is picked.
  const appId = app === ALL ? '' : app
  const servicesQuery = useServices(appId)
  // Picking a different app clears the (now-irrelevant) service filter.
  const onAppChange = (next: string) => {
    setApp(next)
    setService(ALL)
  }

  const filters: LogSearchFilters = {
    app: appId,
    service: service === ALL ? '' : service,
    stream: stream === 'all' ? '' : stream,
    q: query,
  }

  const { data, isLoading, isError, refetch, fetchNextPage, hasNextPage, isFetchingNextPage } =
    useLogSearch(filters)

  // Pages arrive newest-first; flatten then reverse to chronological order so
  // the viewer reads oldest→newest (newest pinned at the bottom).
  const lines: LogLine[] = useMemo(() => {
    const rows = (data?.pages ?? []).flatMap((p) => p.logs)
    const showApp = appId === ''
    return rows
      .slice()
      .reverse()
      .map((r) => {
        const appLabel = showApp ? (appNames.get(r.app_id) ?? r.app_id.slice(0, 8)) : null
        return {
          key: String(r.id),
          ts: r.at,
          service: appLabel ? `${appLabel}·${r.service}` : r.service,
          stream: r.stream,
          level: levelFor(r.stream, r.message),
          message: r.message,
        }
      })
  }, [data, appId, appNames])

  return (
    <PageContainer>
      <PageHeader title={t('explorer.title')} description={t('explorer.description')} />

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex h-8 max-w-72 flex-1 basis-56 items-center gap-2 rounded-md border bg-background px-3">
          <Search className="size-3.5 text-muted-foreground" />
          <input
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={t('explorer.searchPlaceholder')}
            aria-label={t('explorer.searchAria')}
            className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
          />
        </div>

        <Select value={app} onValueChange={onAppChange}>
          <SelectTrigger size="sm" className="w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>{t('explorer.allApps')}</SelectItem>
            {apps.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        {appId && (servicesQuery.data?.length ?? 0) > 0 ? (
          <Select value={service} onValueChange={setService}>
            <SelectTrigger size="sm" className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('explorer.allServices')}</SelectItem>
              {servicesQuery.data!.map((s) => (
                <SelectItem key={s.id} value={s.name}>
                  {s.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}

        <Select value={stream} onValueChange={setStream}>
          <SelectTrigger size="sm" className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {STREAMS.map((s) => (
              <SelectItem key={s} value={s}>
                {t(`explorer.streams.${s}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <span className="ml-auto font-mono text-2xs text-muted-foreground">
          {t('explorer.matchCount', { count: lines.length })}
        </span>
      </div>

      {isError ? (
        <ErrorState message={t('explorer.error')} onRetry={() => refetch()} />
      ) : isLoading ? (
        <Skeleton className="h-112 w-full rounded-xl" />
      ) : (
        <div className="flex flex-col gap-3">
          {hasNextPage ? (
            <Button
              variant="outline"
              size="sm"
              className="self-center"
              disabled={isFetchingNextPage}
              onClick={() => fetchNextPage()}
            >
              {isFetchingNextPage ? t('explorer.loadingOlder') : t('explorer.loadOlder')}
            </Button>
          ) : lines.length > 0 ? (
            <span className="self-center text-2xs text-muted-foreground">
              {t('explorer.noMore')}
            </span>
          ) : null}

          <LogViewer
            lines={lines}
            autoScroll={false}
            label={t('explorer.viewerLabel')}
            emptyLabel={query || appId ? t('explorer.empty') : t('explorer.emptyHint')}
          />
        </div>
      )}
    </PageContainer>
  )
}
