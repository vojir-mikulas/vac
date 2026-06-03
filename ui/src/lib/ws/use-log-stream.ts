import { useCallback, useEffect, useRef, useState } from 'react'

import { useWebSocket } from '@/lib/ws/use-websocket'
import type { BuildLogData, RuntimeLogData, WsFrame } from '@/types/api'

export type LogLevel = 'info' | 'warn' | 'error' | 'ok'

export interface LogLine {
  key: string
  ts: string
  service: string | null
  stream: string
  level: LogLevel
  message: string
}

const RING_BUFFER_LINES = 2000

function levelFor(stream: string, message: string): LogLevel {
  if (stream === 'stderr') return 'error'
  const m = message.toLowerCase()
  if (m.includes('error') || m.includes('panic') || m.includes('fatal')) return 'error'
  if (m.includes('warn')) return 'warn'
  return 'info'
}

// Shared accumulator: keeps a bounded ring buffer of lines in state and a
// monotonic counter in a ref so keys stay stable without re-subscribing.
function useLogAccumulator() {
  const [lines, setLines] = useState<LogLine[]>([])
  const [done, setDone] = useState(false)
  const counter = useRef(0)

  const push = useCallback((line: Omit<LogLine, 'key'>) => {
    const key = `${line.ts}-${counter.current++}`
    setLines((prev) => {
      const next = prev.length >= RING_BUFFER_LINES ? prev.slice(1) : prev.slice()
      next.push({ ...line, key })
      return next
    })
  }, [])

  const clear = useCallback(() => {
    setLines([])
    setDone(false)
    counter.current = 0
  }, [])

  return { lines, done, setDone, push, clear }
}

// Live build-log stream for one deployment. WS replays persisted lines then
// tails; a `build-end` frame flips `done` and tears the socket down so it
// never reconnects/re-replays (the source of the old "error spam" loop).
// `onDone` fires exactly once when the stream terminates — callers use it to
// settle the deployment's cached status.
export function useDeploymentLogs(did: string, enabled = true, onDone?: () => void) {
  const { lines, done, setDone, push } = useLogAccumulator()

  const onDoneRef = useRef(onDone)
  useEffect(() => {
    onDoneRef.current = onDone
  })
  const doneFiredRef = useRef(false)

  const onFrame = useCallback(
    (frame: WsFrame) => {
      if (frame.type === 'build-end') {
        setDone(true)
        if (!doneFiredRef.current) {
          doneFiredRef.current = true
          onDoneRef.current?.()
        }
        return
      }
      if (frame.type === 'build') {
        const d = frame.data as BuildLogData
        push({
          ts: frame.ts,
          service: d.service_name ?? frame.service ?? null,
          stream: d.stream,
          level: levelFor(d.stream, d.message),
          message: d.message,
        })
      }
    },
    [push, setDone],
  )

  // Disable once done so the socket closes and does not reconnect/replay.
  const status = useWebSocket(`/api/deployments/${did}/logs`, {
    enabled: enabled && !done,
    onFrame,
  })
  return { lines, done, status }
}

// Live runtime-log stream for an app (optionally one service).
export function useRuntimeLogs(appId: string, service?: string, enabled = true) {
  const { lines, push } = useLogAccumulator()

  const onFrame = useCallback(
    (frame: WsFrame) => {
      if (frame.type !== 'log') return
      const d = frame.data as RuntimeLogData
      push({
        ts: frame.ts,
        service: frame.service ?? service ?? null,
        stream: d.stream,
        level: levelFor(d.stream, d.message),
        message: d.message,
      })
    },
    [push, service],
  )

  const path = service ? `/api/apps/${appId}/services/${service}/logs` : `/api/apps/${appId}/logs`

  const status = useWebSocket(path, { enabled, onFrame })
  return { lines, status }
}
