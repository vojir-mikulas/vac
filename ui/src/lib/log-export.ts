import type { LogLine } from '@/lib/ws/use-log-stream'

export function logsToText(lines: LogLine[]): string {
  return lines
    .map((l) => {
      const svc = l.service ? ` [${l.service}]` : ''
      return `${l.ts}${svc} ${l.level.toUpperCase()} ${l.message}`
    })
    .join('\n')
}

export function logsToJson(lines: LogLine[]): string {
  return JSON.stringify(
    lines.map(({ ts, service, level, stream, message }) => ({
      ts,
      service,
      level,
      stream,
      message,
    })),
    null,
    2,
  )
}

export function downloadFile(filename: string, content: string, mime: string) {
  const blob = new Blob([content], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  // Append before clicking (some browsers ignore clicks on detached anchors)
  // and defer the revoke so it can't race/cancel the in-flight download.
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
