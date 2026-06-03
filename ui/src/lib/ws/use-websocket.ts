import { useEffect, useRef, useState } from 'react'

import type { WsFrame } from '@/types/api'

function wsUrl(path: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const p = path.startsWith('/') ? path : `/api/${path}`
  return `${proto}://${window.location.host}${p}`
}

// Connection lifecycle a passive stream can surface to the operator:
//   connecting   — first attempt (or a resume after the tab became visible)
//   open         — socket established, frames flowing
//   reconnecting — dropped, a backoff retry is scheduled
//   closed       — disabled, torn down, or paused while the tab is hidden
export type WsStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

interface Options {
  enabled?: boolean
  onFrame: (frame: WsFrame) => void
  onOpen?: () => void
}

const MAX_BACKOFF_MS = 10_000

// Generic server-push WebSocket consumer. Handles reconnect with backoff and
// pauses while the tab is hidden. Handlers are stored in refs so a changing
// callback identity never tears down the socket (advanced-event-handler-refs).
// Returns the live connection status for an optional ConnectionBadge — the
// socket lifecycle itself is unchanged; status is a read-only side output.
export function useWebSocket(path: string, options: Options): WsStatus {
  const { enabled = true, onFrame, onOpen } = options
  const onFrameRef = useRef(onFrame)
  const onOpenRef = useRef(onOpen)
  const [status, setStatus] = useState<WsStatus>(enabled ? 'connecting' : 'closed')

  useEffect(() => {
    onFrameRef.current = onFrame
    onOpenRef.current = onOpen
  })

  useEffect(() => {
    if (!enabled) return

    let socket: WebSocket | null = null
    let backoff = 500
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let closedByCaller = false

    const connect = () => {
      if (document.hidden) return
      setStatus('connecting')
      socket = new WebSocket(wsUrl(path))

      socket.onopen = () => {
        backoff = 500
        setStatus('open')
        onOpenRef.current?.()
      }
      socket.onmessage = (event) => {
        try {
          onFrameRef.current(JSON.parse(event.data as string) as WsFrame)
        } catch {
          // ignore malformed frames
        }
      }
      socket.onclose = () => {
        socket = null
        if (closedByCaller || document.hidden) {
          setStatus('closed')
          return
        }
        setStatus('reconnecting')
        reconnectTimer = setTimeout(connect, backoff)
        backoff = Math.min(backoff * 2, MAX_BACKOFF_MS)
      }
      socket.onerror = () => socket?.close()
    }

    const onVisibility = () => {
      if (document.hidden) {
        socket?.close()
      } else if (!socket) {
        clearTimeout(reconnectTimer)
        connect()
      }
    }

    connect()
    document.addEventListener('visibilitychange', onVisibility)

    return () => {
      closedByCaller = true
      clearTimeout(reconnectTimer)
      document.removeEventListener('visibilitychange', onVisibility)
      socket?.close()
      // Reflect teardown for the next render (disabled/unmounted/path change).
      // socket.onclose also lands here when a connection was live; this covers
      // the case where the effect tears down with only a pending reconnect.
      setStatus('closed')
    }
  }, [path, enabled])

  return status
}
