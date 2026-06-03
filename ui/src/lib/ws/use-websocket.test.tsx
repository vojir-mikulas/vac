import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, cleanup } from '@testing-library/react'

import { useWebSocket, type WsStatus } from '@/lib/ws/use-websocket'

// A controllable WebSocket stand-in: tests drive open/close/error manually so we
// can assert the status machine (connecting → open → reconnecting → … → closed)
// without timers racing real I/O.
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  readyState = 0
  url: string
  onopen: (() => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  open() {
    this.readyState = 1
    this.onopen?.()
  }
  drop() {
    this.readyState = 3
    this.onclose?.()
  }
  close() {
    this.readyState = 3
    this.onclose?.()
  }
}

function latestSocket(): FakeWebSocket {
  const s = FakeWebSocket.instances.at(-1)
  if (!s) throw new Error('no socket created')
  return s
}

// Probe renders the hook and reports the latest status out via a callback.
function Probe({ enabled, onStatus }: { enabled: boolean; onStatus: (s: WsStatus) => void }) {
  const status = useWebSocket('/api/test', { enabled, onFrame: () => {} })
  onStatus(status)
  return null
}

describe('useWebSocket status', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.useFakeTimers()
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('reports connecting then open, and reconnecting after a drop', () => {
    let status: WsStatus = 'closed'
    const onStatus = (s: WsStatus) => {
      status = s
    }

    render(<Probe enabled onStatus={onStatus} />)
    // First render: the effect kicks off a connection attempt.
    expect(status).toBe('connecting')
    expect(FakeWebSocket.instances).toHaveLength(1)

    act(() => latestSocket().open())
    expect(status).toBe('open')

    // A drop (not by the caller) schedules a backoff retry → reconnecting.
    act(() => latestSocket().drop())
    expect(status).toBe('reconnecting')

    // After the backoff fires, a fresh socket is created and we're connecting.
    act(() => vi.advanceTimersByTime(500))
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(status).toBe('connecting')
  })

  it('is closed when disabled', () => {
    let status: WsStatus = 'open'
    render(<Probe enabled={false} onStatus={(s) => (status = s)} />)
    expect(status).toBe('closed')
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it('goes closed when toggled from enabled to disabled', () => {
    let status: WsStatus = 'closed'
    const onStatus = (s: WsStatus) => {
      status = s
    }
    const { rerender } = render(<Probe enabled onStatus={onStatus} />)
    act(() => latestSocket().open())
    expect(status).toBe('open')

    // Disabling tears down the effect; the live socket's close handler reports
    // the closed state on the next render.
    act(() => rerender(<Probe enabled={false} onStatus={onStatus} />))
    expect(status).toBe('closed')
  })
})
