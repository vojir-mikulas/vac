// Replaces the global WebSocket so the UI's single `new WebSocket()` call site
// (lib/ws/use-websocket.ts) gets simulated server-push streams instead of a
// real socket. Routes by URL path: deploy build logs, per-app runtime logs,
// per-service runtime logs, and live per-service stats.

import type { WsFrame } from '@/types/api'
import {
  deployLogTopic,
  findApp,
  findDeployment,
  runtimeLogFrame,
  serviceStats,
  subscribe,
} from './db'
import { isDeployTerminal } from '@/lib/deploy-status'

const STATS_INTERVAL_MS = 2_000
const RUNTIME_LOG_INTERVAL_MS = 1_600

type Timer = ReturnType<typeof setInterval>

class MockWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSING = 2
  readonly CLOSED = 3

  readyState = 0
  url: string
  binaryType: BinaryType = 'blob'
  bufferedAmount = 0
  extensions = ''
  protocol = ''

  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  private timers: Timer[] = []
  private unsubscribe: (() => void) | null = null
  private openTimer: ReturnType<typeof setTimeout>

  constructor(url: string | URL) {
    this.url = typeof url === 'string' ? url : url.href
    this.openTimer = setTimeout(() => this.handleOpen(), 60)
  }

  private handleOpen(): void {
    if (this.readyState === this.CLOSED) return
    this.readyState = this.OPEN
    this.onopen?.(new Event('open'))
    this.startStream()
  }

  private emit(frame: WsFrame): void {
    if (this.readyState !== this.OPEN) return
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }

  private startStream(): void {
    let path: string
    try {
      path = new URL(this.url, window.location.origin).pathname
    } catch {
      return
    }

    const deployMatch = path.match(/^\/api\/deployments\/([^/]+)\/logs$/)
    if (deployMatch) return this.streamDeployLogs(deployMatch[1]!)

    const statsMatch = path.match(/^\/api\/apps\/([^/]+)\/stats$/)
    if (statsMatch) return this.streamStats(statsMatch[1]!)

    const svcLogMatch = path.match(/^\/api\/apps\/([^/]+)\/services\/([^/]+)\/logs$/)
    if (svcLogMatch) return this.streamRuntimeLogs(svcLogMatch[1]!, svcLogMatch[2]!)

    const appLogMatch = path.match(/^\/api\/apps\/([^/]+)\/logs$/)
    if (appLogMatch) return this.streamRuntimeLogs(appLogMatch[1]!, null)
  }

  private streamDeployLogs(did: string): void {
    const found = findDeployment(did)
    if (!found) {
      this.emit({ type: 'build-end', ts: new Date().toISOString() })
      return
    }
    const { dep } = found

    // Replay everything buffered so far as build frames.
    for (const line of dep.logs) {
      this.emit({
        type: 'build',
        id: line.id,
        ts: line.ts,
        data: { stream: line.stream, message: line.message, service_name: line.service_name },
      })
    }

    if (isDeployTerminal(dep.status)) {
      this.emit({ type: 'build-end', ts: new Date().toISOString() })
      return
    }

    // Tail live frames published by the deploy scheduler.
    this.unsubscribe = subscribe(deployLogTopic(did), (frame) => this.emit(frame))
  }

  private streamStats(appId: string): void {
    const tick = () => {
      const app = findApp(appId)
      if (!app) return
      for (const s of app.services) {
        if (s.status !== 'running') continue
        this.emit({
          type: 'stats',
          ts: new Date().toISOString(),
          service: s.name,
          data: serviceStats(),
        })
      }
    }
    tick()
    this.timers.push(setInterval(tick, STATS_INTERVAL_MS))
  }

  private streamRuntimeLogs(appId: string, service: string | null): void {
    const tick = () => {
      const app = findApp(appId)
      if (!app) return
      const running = app.services.filter((s) => s.status === 'running')
      if (running.length === 0) return
      const target = service ?? running[Math.floor(Math.random() * running.length)]!.name
      this.emit(runtimeLogFrame(target))
    }
    this.timers.push(setInterval(tick, RUNTIME_LOG_INTERVAL_MS))
  }

  send(): void {
    // The UI never sends; ignore.
  }

  close(): void {
    if (this.readyState === this.CLOSED) return
    this.readyState = this.CLOSED
    clearTimeout(this.openTimer)
    this.timers.forEach(clearInterval)
    this.timers = []
    this.unsubscribe?.()
    this.unsubscribe = null
    this.onclose?.(new CloseEvent('close', { wasClean: true }))
  }

  addEventListener(): void {}
  removeEventListener(): void {}
  dispatchEvent(): boolean {
    return true
  }
}

export function installWebSocketMock(): void {
  // The UI only uses property handlers + close(), so this structural stand-in
  // is sufficient. Cast through unknown since we intentionally don't implement
  // the full WebSocket DOM interface.
  ;(globalThis as unknown as { WebSocket: unknown }).WebSocket = MockWebSocket as unknown
}
