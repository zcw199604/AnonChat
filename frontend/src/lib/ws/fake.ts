/** 测试用假 WebSocket：可手动触发 open/message/close，并记录发送帧。 */

export interface FakeWSInstance {
  url: string
  readyState: number
  sent: Array<Record<string, unknown>>
  onopen: ((ev: unknown) => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: unknown) => void) | null
  onerror: ((ev: unknown) => void) | null
  send(data: string): void
  close(): void
  // 测试辅助
  open(): void
  message(obj: unknown): void
  serverClose(): void
}

export interface FakeWebSocketFactory {
  Impl: new (url: string) => FakeWSInstance
  instances: FakeWSInstance[]
  latest(): FakeWSInstance
}

export function createFakeWebSocket(): FakeWebSocketFactory {
  const instances: FakeWSInstance[] = []

  class FakeWS implements FakeWSInstance {
    url: string
    readyState = 0
    sent: Array<Record<string, unknown>> = []
    onopen: ((ev: unknown) => void) | null = null
    onmessage: ((ev: { data: string }) => void) | null = null
    onclose: ((ev: unknown) => void) | null = null
    onerror: ((ev: unknown) => void) | null = null

    constructor(url: string) {
      this.url = url
      instances.push(this)
    }

    send(data: string): void {
      this.sent.push(JSON.parse(data) as Record<string, unknown>)
    }

    close(): void {
      this.readyState = 3
      this.onclose?.({})
    }

    open(): void {
      this.readyState = 1
      this.onopen?.({})
    }

    message(obj: unknown): void {
      this.onmessage?.({ data: JSON.stringify(obj) })
    }

    serverClose(): void {
      this.readyState = 3
      this.onclose?.({})
    }
  }

  return {
    Impl: FakeWS as unknown as new (url: string) => FakeWSInstance,
    instances,
    latest(): FakeWSInstance {
      return instances[instances.length - 1]
    },
  }
}
