import { useDeferredValue, useMemo, useState } from 'react'
import { Download } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { LogViewer } from '@/components/common/log-viewer'
import { downloadFile, logsToJson, logsToText } from '@/lib/log-export'
import type { LogLevel, LogLine } from '@/lib/ws/use-log-stream'

const LEVELS = ['all', 'info', 'warn', 'error'] as const satisfies readonly string[]

const LEVEL_RANK: Record<LogLevel, number> = { info: 0, ok: 0, warn: 1, error: 2 }

export function LogPanel({
  lines,
  services,
  initialService,
  exportName = 'logs',
  status,
}: {
  lines: LogLine[]
  services?: string[]
  initialService?: string
  exportName?: string
  status?: React.ReactNode
}) {
  const { t } = useTranslation('logs')
  const [autoScroll, setAutoScroll] = useState(true)
  const [level, setLevel] = useState('all')
  const [service, setService] = useState(initialService ?? 'all')
  const deferredLines = useDeferredValue(lines)

  const filtered = useMemo(() => {
    const minRank = level === 'all' ? -1 : LEVEL_RANK[level as LogLevel]
    return deferredLines.filter((l) => {
      if (service !== 'all' && l.service !== service) return false
      if (minRank >= 0 && LEVEL_RANK[l.level] < minRank) return false
      return true
    })
  }, [deferredLines, level, service])

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        {services && services.length > 0 ? (
          <Select value={service} onValueChange={setService}>
            <SelectTrigger size="sm" className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t('panel.allServices')}</SelectItem>
              {services.map((s) => (
                <SelectItem key={s} value={s}>
                  {s}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}

        <Select value={level} onValueChange={setLevel}>
          <SelectTrigger size="sm" className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {LEVELS.map((l) => (
              <SelectItem key={l} value={l}>
                {t(`panel.levels.${l}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <Switch checked={autoScroll} onCheckedChange={setAutoScroll} />
          {t('panel.autoScroll')}
        </label>

        <div className="ml-auto flex items-center gap-3">
          {status}
          <span className="font-mono text-2xs text-muted-foreground">
            {t('panel.lineCount', { count: filtered.length })}
          </span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Download className="size-3.5" />
                {t('panel.export')}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                onSelect={() =>
                  downloadFile(`${exportName}.txt`, logsToText(filtered), 'text/plain')
                }
              >
                {t('panel.exportText')}
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() =>
                  downloadFile(`${exportName}.json`, logsToJson(filtered), 'application/json')
                }
              >
                {t('panel.exportJson')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <LogViewer lines={filtered} autoScroll={autoScroll} label={t('panel.viewerLabel')} />
    </div>
  )
}
