import { useDeferredValue, useMemo, useState } from 'react'
import { Download } from 'lucide-react'

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

const LEVELS: { value: string; label: string }[] = [
  { value: 'all', label: 'All levels' },
  { value: 'info', label: 'Info' },
  { value: 'warn', label: 'Warn' },
  { value: 'error', label: 'Error' },
]

const LEVEL_RANK: Record<LogLevel, number> = { info: 0, ok: 0, warn: 1, error: 2 }

export function LogPanel({
  lines,
  services,
  exportName = 'logs',
}: {
  lines: LogLine[]
  services?: string[]
  exportName?: string
}) {
  const [autoScroll, setAutoScroll] = useState(true)
  const [level, setLevel] = useState('all')
  const [service, setService] = useState('all')
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
              <SelectItem value="all">All services</SelectItem>
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
              <SelectItem key={l.value} value={l.value}>
                {l.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <Switch checked={autoScroll} onCheckedChange={setAutoScroll} />
          Auto-scroll
        </label>

        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-2xs text-muted-foreground">{filtered.length} lines</span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Download className="size-3.5" />
                Export
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                onSelect={() =>
                  downloadFile(`${exportName}.txt`, logsToText(filtered), 'text/plain')
                }
              >
                Plain text
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() =>
                  downloadFile(`${exportName}.json`, logsToJson(filtered), 'application/json')
                }
              >
                JSON
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <LogViewer lines={filtered} autoScroll={autoScroll} />
    </div>
  )
}
