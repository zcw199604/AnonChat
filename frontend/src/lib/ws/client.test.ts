import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsClient } from './client'
import type { ResumeApi } from './client'
import { createFakeWebSocket } from './fake'
import { ApiError } from '../api/client'

function makeResumeApi(): ResumeApi & {
  getConversationMessages: ReturnType<typeof vi.fn>
  getRoomMessages: ReturnType<typeof vi.fn>
} {
  return {
    getConversationMessages: vi.fn(async () => []),
    getRoomMessages: vi.fn(async () => []),
  } as unknown as ResumeApi & {
    getConversationMessages: ReturnType<typeof vi.fn>
    getRoomMessages: ReturnType<typeof vi.fn>
  }
}

describe('WsClient', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  function setup() {
    const fake = createFakeWebSocket()
    const resume = makeResumeApi()
    const client = new WsClient({
      url: 'ws://test/ws',
      WebSocketImpl: fake.Impl,
      resume,
      random: () => 0.5,
    })
    return { fake, resume, client }
  }

  it('request 在收到同 id 的 ack 时 resolve', async () => {
    const { fake, client } = setup()
    client.connect()
    fake.latest().open()

    const p = client.request('ping', { a: 1 })
    const frame = fake.latest().sent[0]
    expect(frame.t).toBe('ping')
    fake.latest().message({ t: 'ack', id: frame.id, d: { ok: true } })
    await expect(p).resolves.toEqual({ ok: true })
  })

  it('request 在收到同 id 的 error 时 reject 为 ApiError', async () => {
    const { fake, client } = setup()
    client.connect()
    fake.latest().open()

    const p = client.request('msg.send', {})
    const frame = fake.latest().sent[0]
    fake.latest().message({ t: 'error', id: frame.id, d: { code: 'rate_limited', message: 'too fast' } })
    await expect(p).rejects.toBeInstanceOf(ApiError)
    await expect(p).rejects.toMatchObject({ code: 'rate_limited' })
  })

  it('request 超时 10 秒后 reject', async () => {
    const { fake, client } = setup()
    client.connect()
    fake.latest().open()

    const p = client.request('ping', {})
    p.catch(() => undefined)
    await vi.advanceTimersByTimeAsync(10_000)
    await expect(p).rejects.toMatchObject({ message: '请求超时' })
  })

  it('断线后按 1s、2s、4s 指数退避重连（抖动由固定随机抵消）', async () => {
    const { fake, client } = setup()
    client.connect()
    fake.latest().open()
    expect(fake.instances).toHaveLength(1)

    // 连接失败（未 open）后连续重试：退避应递增 1s、2s、4s。
    fake.latest().serverClose()
    await vi.advanceTimersByTimeAsync(999)
    expect(fake.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(fake.instances).toHaveLength(2)

    fake.latest().serverClose() // 第 2 次仍未成功连接
    await vi.advanceTimersByTimeAsync(1999)
    expect(fake.instances).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(fake.instances).toHaveLength(3)

    fake.latest().serverClose() // 第 3 次仍未成功连接
    await vi.advanceTimersByTimeAsync(3999)
    expect(fake.instances).toHaveLength(3)
    await vi.advanceTimersByTimeAsync(1)
    expect(fake.instances).toHaveLength(4)

    // 成功连接后退避重置：再次断线应回到 1s。
    fake.latest().open()
    fake.latest().serverClose()
    await vi.advanceTimersByTimeAsync(1000)
    expect(fake.instances).toHaveLength(5)
  })

  it('连接成功后立即发送心跳 ping（每 25 秒）', async () => {
    const { fake, client } = setup()
    client.connect()
    fake.latest().open()

    await vi.advanceTimersByTimeAsync(25_000)
    const pings = fake.latest().sent.filter((f) => f.t === 'ping')
    expect(pings.length).toBe(1)
    await vi.advanceTimersByTimeAsync(25_000)
    expect(fake.latest().sent.filter((f) => f.t === 'ping').length).toBe(2)
  })

  it('收到 kicked 后停止重连并广播原因', async () => {
    const { fake, client } = setup()
    const onKicked = vi.fn()
    client.on('kicked', onKicked)
    client.connect()
    fake.latest().open()

    fake.latest().message({ t: 'kicked', d: { reason: 'banned' } })
    expect(onKicked).toHaveBeenCalledWith({ reason: 'banned' }, expect.anything())

    fake.latest().serverClose()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fake.instances).toHaveLength(1) // 不再新建连接
  })

  it('发件箱状态 sending → sent / failed，retry 复用 clientMsgId', async () => {
    const { fake, client } = setup()
    const statuses: string[] = []
    client.on('outbox', (d) => statuses.push((d as { status: string }).status))
    client.connect()
    fake.latest().open()

    const { clientMsgId } = client.sendMessage('c1', 'hello')
    expect(client.getOutbox()[0].status).toBe('sending')
    let frame = fake.latest().sent.find((f) => f.t === 'msg.send')!
    expect((frame.d as { clientMsgId: string }).clientMsgId).toBe(clientMsgId)
    fake.latest().message({ t: 'ack', id: frame.id, d: { messageId: 42 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(client.getOutbox()[0].status).toBe('sent')
    expect(client.getOutbox()[0].messageId).toBe(42)

    // 失败 + 重试
    const second = client.sendMessage('c1', 'again')
    frame = [...fake.latest().sent].reverse().find((f) => f.t === 'msg.send')!
    fake.latest().message({ t: 'error', id: frame.id, d: { code: 'content_blocked', message: 'x' } })
    await vi.advanceTimersByTimeAsync(0)
    const failed = client.getOutbox().find((i) => i.clientMsgId === second.clientMsgId)!
    expect(failed.status).toBe('failed')

    client.retry(second.clientMsgId)
    const retryFrame = [...fake.latest().sent].reverse().find((f) => f.t === 'msg.send')!
    expect((retryFrame.d as { clientMsgId: string }).clientMsgId).toBe(second.clientMsgId)
  })

  it('重连后重新订阅、重新加入房间并按最后消息 id 补拉、去重', async () => {
    const { fake, resume, client } = setup()
    const newMessages: number[] = []
    client.on('msg.new', (d) => newMessages.push((d as { message: { id: number } }).message.id))

    client.connect()
    fake.latest().open()
    client.subscribeConversation('c1')
    client.joinRoom('r1')

    // 收到一条 id=5 的实时消息，更新本地最后消息 id。
    fake.latest().message({ t: 'msg.new', d: { conversationId: 'c1', message: { id: 5 } } })
    expect(newMessages).toEqual([5])

    // 配置补拉：会话返回重复的 5 与新消息 6；房间返回 100。
    resume.getConversationMessages.mockResolvedValueOnce([{ id: 5 }, { id: 6 }])
    resume.getRoomMessages.mockResolvedValueOnce([{ id: 100 }])

    // 断线重连。
    fake.latest().serverClose()
    await vi.advanceTimersByTimeAsync(1000)
    fake.latest().open()
    await vi.advanceTimersByTimeAsync(0)

    // 补拉按最后消息 id=5 请求。
    expect(resume.getConversationMessages).toHaveBeenCalledWith('c1', 5)
    expect(resume.getRoomMessages).toHaveBeenCalledWith('r1')
    // 去重：5 不重复，新增 6、100。
    expect(newMessages).toEqual([5, 6, 100])
    // 重连后重新下发了 conv.subscribe 与 room.join。
    const sentTypes = fake.latest().sent.map((f) => f.t)
    expect(sentTypes).toContain('conv.subscribe')
    expect(sentTypes).toContain('room.join')
  })
})
