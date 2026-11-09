/** WebSocket 客户端：请求/响应、事件订阅、重连退避、心跳、发件箱与断线恢复。 */
import { ApiError } from '../api/client'
import type { ChatMessage } from '../api/client'

export interface Envelope {
  t: string
  id?: string
  d?: unknown
  ts?: number
}

export type OutboxStatus = 'sending' | 'sent' | 'failed'

export interface OutboxItem {
  clientMsgId: string
  conversationId: string
  body: string
  status: OutboxStatus
  messageId?: number
  errorMessage?: string
}

/** 断线恢复所需的补拉接口。 */
export interface ResumeApi {
  getConversationMessages(conversationId: string, after?: number): Promise<ChatMessage[]>
  getRoomMessages(roomId: string): Promise<ChatMessage[]>
}

interface WebSocketLike {
  readyState: number
  send(data: string): void
  close(): void
  onopen: ((ev: unknown) => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: unknown) => void) | null
  onerror: ((ev: unknown) => void) | null
}

type WebSocketCtor = new (url: string) => WebSocketLike

export type WsHandler = (data: unknown, envelope: Envelope) => void

export interface WsClientOptions {
  url?: string
  WebSocketImpl?: WebSocketCtor
  resume: ResumeApi
  requestTimeoutMs?: number
  heartbeatMs?: number
  maxBackoffMs?: number
  /** 抖动随机源，返回 [0,1)，测试注入固定值。 */
  random?: () => number
}

const WS_OPEN = 1

/** 生成标准 UUID v4（优先 randomUUID，否则用 getRandomValues 自行拼装）。 */
export function uuidv4(): string {
  const bytes = new Uint8Array(16)
  const c = globalThis.crypto
  if (c && typeof c.getRandomValues === 'function') {
    c.getRandomValues(bytes)
  } else {
    for (let i = 0; i < 16; i++) bytes[i] = Math.floor(Math.random() * 256)
  }
  // 设置版本号(4)与变体位
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex: string[] = []
  for (let i = 0; i < 16; i++) hex.push(bytes[i].toString(16).padStart(2, '0'))
  return (
    hex.slice(0, 4).join('') + '-' +
    hex.slice(4, 6).join('') + '-' +
    hex.slice(6, 8).join('') + '-' +
    hex.slice(8, 10).join('') + '-' +
    hex.slice(10, 16).join('')
  )
}

/** 生成请求/客户端消息 id（UUID v4）。 */
export function genId(): string {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID()
  }
  return uuidv4()
}

/** 默认 WS 地址：根据 location 推导 ws/wss。 */
export function defaultWsUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws`
}

interface PendingRequest {
  resolve: (v: unknown) => void
  reject: (e: Error) => void
  timer: ReturnType<typeof setTimeout>
}

/** WsClient 提供 WS 连接管理。 */
export class WsClient {
  private readonly url: string
  private readonly WS: WebSocketCtor
  private readonly resumeApi: ResumeApi
  private readonly requestTimeoutMs: number
  private readonly heartbeatMs: number
  private readonly maxBackoffMs: number
  private readonly random: () => number

  private ws: WebSocketLike | null = null
  private closedByUser = false
  private kicked = false
  private openCount = 0
  private backoffAttempt = 0
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null

  private pending = new Map<string, PendingRequest>()
  private handlers = new Map<string, Set<WsHandler>>()
  private outbox = new Map<string, OutboxItem>()

  private subscribed = new Set<string>()
  private joinedRooms = new Set<string>()
  private lastMessageId = new Map<string, number>()
  private delivered = new Map<string, Set<number>>()

  constructor(opts: WsClientOptions) {
    this.url = opts.url ?? defaultWsUrl()
    this.WS =
      opts.WebSocketImpl ??
      (WebSocket as unknown as WebSocketCtor)
    this.resumeApi = opts.resume
    this.requestTimeoutMs = opts.requestTimeoutMs ?? 10_000
    this.heartbeatMs = opts.heartbeatMs ?? 25_000
    this.maxBackoffMs = opts.maxBackoffMs ?? 30_000
    this.random = opts.random ?? Math.random
  }

  /** 建立连接。 */
  connect(): void {
    this.closedByUser = false
    const ws = new this.WS(this.url)
    this.ws = ws
    ws.onopen = () => this.handleOpen()
    ws.onmessage = (ev) => this.handleMessage(ev.data)
    ws.onclose = () => this.handleClose()
    ws.onerror = () => {
      try {
        ws.close()
      } catch {
        // 忽略
      }
    }
  }

  /** 主动关闭，不再重连。 */
  close(): void {
    this.closedByUser = true
    this.stopHeartbeat()
    this.ws?.close()
  }

  isOpen(): boolean {
    return this.ws?.readyState === WS_OPEN
  }

  /** 订阅服务端事件，返回取消订阅函数。 */
  on(type: string, handler: WsHandler): () => void {
    let set = this.handlers.get(type)
    if (!set) {
      set = new Set()
      this.handlers.set(type, set)
    }
    set.add(handler)
    return () => {
      set?.delete(handler)
    }
  }

  /** 发送请求并等待同 id 的 ack/error。 */
  request(type: string, data?: unknown): Promise<unknown> {
    if (!this.isOpen()) {
      return Promise.reject(new ApiError('internal', '连接尚未就绪', 0))
    }
    const id = genId()
    return this.sendRequestWithID(id, type, data)
  }

  private sendRequestWithID(id: string, type: string, data?: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new ApiError('internal', '请求超时', 0))
      }, this.requestTimeoutMs)
      this.pending.set(id, { resolve, reject, timer })
      this.sendFrame({ t: type, id, d: data })
    })
  }

  /** 发送一条不需要等待 ack 的消息（如 match.start / match.next / conv.subscribe）。 */
  send(type: string, data?: unknown): void {
    this.sendFrame({ t: type, id: genId(), d: data })
  }

  /** 订阅会话（供恢复使用）。 */
  subscribeConversation(conversationId: string): void {
    this.subscribed.add(conversationId)
    this.sendFrame({ t: 'conv.subscribe', id: genId(), d: { conversationIds: [conversationId] } })
  }

  /** 加入房间（供恢复使用）。 */
  joinRoom(roomId: string): void {
    this.joinedRooms.add(roomId)
    this.sendFrame({ t: 'room.join', id: genId(), d: { roomId } })
  }

  /** 发送聊天消息，返回 clientMsgId；状态通过 outbox 事件更新。 */
  sendMessage(conversationId: string, body: string): { clientMsgId: string } {
    const clientMsgId = genId()
    const item: OutboxItem = { clientMsgId, conversationId, body, status: 'sending' }
    this.outbox.set(clientMsgId, item)
    this.emitOutbox(item)
    this.dispatchSend(item)
    return { clientMsgId }
  }

  /** 重试发送：复用同一个 clientMsgId（服务端幂等）。 */
  retry(clientMsgId: string): void {
    const item = this.outbox.get(clientMsgId)
    if (!item) return
    item.status = 'sending'
    item.errorMessage = undefined
    this.emitOutbox(item)
    this.dispatchSend(item)
  }

  /** 返回当前发件箱快照。 */
  getOutbox(): OutboxItem[] {
    return [...this.outbox.values()].map((i) => ({ ...i }))
  }

  private dispatchSend(item: OutboxItem): void {
    const id = genId()
    this.sendRequestWithID(id, 'msg.send', {
      conversationId: item.conversationId,
      clientMsgId: item.clientMsgId,
      kind: 'text',
      body: item.body,
    })
      .then((res) => {
        const d = res as { messageId?: number } | undefined
        item.status = 'sent'
        item.messageId = d?.messageId
        this.emitOutbox(item)
      })
      .catch((e: unknown) => {
        item.status = 'failed'
        item.errorMessage = e instanceof Error ? e.message : '发送失败'
        this.emitOutbox(item)
        if (e instanceof ApiError && e.code === 'rate_limited') {
          this.emit('error', { code: e.code, message: e.message })
        }
      })
  }

  private emitOutbox(item: OutboxItem): void {
    this.emit('outbox', { ...item })
  }

  private sendFrame(env: Envelope): void {
    if (this.ws && this.ws.readyState === WS_OPEN) {
      this.ws.send(JSON.stringify(env))
    }
  }

  private handleOpen(): void {
    this.openCount++
    this.backoffAttempt = 0
    this.startHeartbeat()
    this.emit('open', undefined)
    if (this.openCount > 1) {
      void this.resume()
    }
  }

  private handleClose(): void {
    this.stopHeartbeat()
    for (const [id, p] of this.pending) {
      clearTimeout(p.timer)
      p.reject(new ApiError('internal', '连接已断开', 0))
      this.pending.delete(id)
    }
    this.emit('close', undefined)
    if (this.closedByUser || this.kicked) {
      return
    }
    this.scheduleReconnect()
  }

  private scheduleReconnect(): void {
    this.backoffAttempt++
    const base = Math.min(1000 * 2 ** (this.backoffAttempt - 1), this.maxBackoffMs)
    // ±20% 抖动
    const jitter = 1 + (this.random() * 2 - 1) * 0.2
    const delay = Math.round(base * jitter)
    setTimeout(() => {
      if (!this.closedByUser && !this.kicked) {
        this.connect()
      }
    }, delay)
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      this.sendFrame({ t: 'ping', id: genId() })
    }, this.heartbeatMs)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private handleMessage(raw: string): void {
    let env: Envelope
    try {
      env = JSON.parse(raw) as Envelope
    } catch {
      return
    }
    if (env.t === 'pong') return

    if (env.id) {
      const pending = this.pending.get(env.id)
      if (pending) {
        clearTimeout(pending.timer)
        this.pending.delete(env.id)
        if (env.t === 'ack') {
          pending.resolve(env.d)
          return
        }
        if (env.t === 'error') {
          const e = env.d as { code?: string; message?: string } | undefined
          pending.reject(new ApiError(e?.code ?? 'internal', e?.message ?? '请求失败', 0))
          return
        }
      }
    }

    if (env.t === 'kicked') {
      this.kicked = true
      this.stopHeartbeat()
      this.emit('kicked', env.d)
      try {
        this.ws?.close()
      } catch {
        // 忽略
      }
      return
    }

    if (env.t === 'msg.new') {
      const d = env.d as { conversationId: string; message: ChatMessage }
      if (!this.deliverMessage(d.conversationId, d.message)) {
        return // 重复消息不再向外 broadcast
      }
    }

    this.emit(env.t, env.d, env)
  }

  private deliverMessage(conversationId: string, message: ChatMessage): boolean {
    let set = this.delivered.get(conversationId)
    if (!set) {
      set = new Set()
      this.delivered.set(conversationId, set)
    }
    if (set.has(message.id)) return false
    set.add(message.id)
    if (set.size > 2000) {
      // 简单裁剪，避免无限增长
      const first = set.values().next().value
      if (first !== undefined) set.delete(first)
    }
    const prev = this.lastMessageId.get(conversationId) ?? 0
    if (message.id > prev) this.lastMessageId.set(conversationId, message.id)
    return true
  }

  /** 断线恢复：重新订阅/加入 + 按本地最后一条消息 id 补拉。 */
  private async resume(): Promise<void> {
    for (const convId of this.subscribed) {
      this.sendFrame({ t: 'conv.subscribe', id: genId(), d: { conversationIds: [convId] } })
    }
    for (const roomId of this.joinedRooms) {
      this.sendFrame({ t: 'room.join', id: genId(), d: { roomId } })
    }

    for (const convId of this.subscribed) {
      try {
        const msgs = await this.resumeApi.getConversationMessages(convId, this.lastMessageId.get(convId))
        for (const m of msgs) {
          if (this.deliverMessage(convId, m)) {
            this.emit('msg.new', { conversationId: convId, message: m })
          }
        }
      } catch {
        // 忽略单会话补拉失败
      }
    }
    for (const roomId of this.joinedRooms) {
      try {
        const msgs = await this.resumeApi.getRoomMessages(roomId)
        for (const m of msgs) {
          if (this.deliverMessage(roomId, m)) {
            this.emit('msg.new', { conversationId: roomId, message: m })
          }
        }
      } catch {
        // 忽略
      }
    }
  }

  private emit(type: string, data: unknown, env?: Envelope): void {
    const set = this.handlers.get(type)
    if (!set) return
    for (const h of set) {
      h(data, env ?? { t: type })
    }
  }
}
