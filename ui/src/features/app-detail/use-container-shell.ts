import { useCallback, useEffect, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'

import '@xterm/xterm/css/xterm.css'

export type ShellStatus = 'idle' | 'connecting' | 'connected' | 'disconnected'

// Drives an interactive container shell (P3.4): an xterm.js terminal bridged to
// the exec WebSocket PTY. Terminal output arrives as binary frames and is
// written straight through (xterm renders the ANSI); keystrokes go back as
// binary stdin; terminal dimensions go as a JSON `resize` control frame. The
// socket is NOT auto-reconnecting — a privileged shell should reconnect only on
// an explicit operator action.
export function useContainerShell(appId: string, service: string) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const [status, setStatus] = useState<ShellStatus>('idle')

  const sendResize = useCallback(() => {
    const term = termRef.current
    const socket = socketRef.current
    if (!term || socket?.readyState !== WebSocket.OPEN) return
    socket.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }))
  }, [])

  const connect = useCallback(() => {
    if (!containerRef.current) return

    // Lazily build the terminal once; reuse it across reconnects so scrollback
    // survives and onData stays wired.
    let term = termRef.current
    if (!term) {
      term = new Terminal({
        fontSize: 13,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        cursorBlink: true,
        theme: { background: '#0a0a0a' },
      })
      const fit = new FitAddon()
      term.loadAddon(fit)
      term.open(containerRef.current)
      const enc = new TextEncoder()
      term.onData((data) => {
        const socket = socketRef.current
        if (socket?.readyState === WebSocket.OPEN) socket.send(enc.encode(data))
      })
      termRef.current = term
      fitRef.current = fit
    }
    fitRef.current?.fit()

    setStatus('connecting')
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const socket = new WebSocket(
      `${proto}://${window.location.host}/api/apps/${appId}/services/${service}/exec`,
    )
    socket.binaryType = 'arraybuffer'
    socketRef.current = socket

    socket.onopen = () => {
      setStatus('connected')
      sendResize()
      termRef.current?.focus()
    }
    socket.onmessage = (e) => {
      if (typeof e.data === 'string') termRef.current?.write(e.data)
      else termRef.current?.write(new Uint8Array(e.data as ArrayBuffer))
    }
    socket.onclose = () => {
      if (socketRef.current === socket) socketRef.current = null
      setStatus('disconnected')
    }
    socket.onerror = () => socket.close()
  }, [appId, service, sendResize])

  const disconnect = useCallback(() => {
    socketRef.current?.close()
    socketRef.current = null
  }, [])

  // Keep the terminal sized to its container.
  useEffect(() => {
    const onResize = () => {
      fitRef.current?.fit()
      sendResize()
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [sendResize])

  // Tear everything down on unmount so no socket or terminal leaks.
  useEffect(() => {
    return () => {
      socketRef.current?.close()
      termRef.current?.dispose()
      termRef.current = null
    }
  }, [])

  return { containerRef, status, connect, disconnect }
}
