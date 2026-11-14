import { StrictMode } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent, within, cleanup } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import * as api from '../../lib/api/client'
import type { Envelope } from '../../lib/ws/client'
import { useConversationsStore } from '../../stores/conversations'
import { useIdentityStore } from '../../stores/identity'
import MatchPage from './MatchPage'

const handlers = vi.hoisted(() => new Map<string, (data: unknown, env: Envelope) => void>())
const ws = vi.hoisted(() => ({
  on: vi.fn((evt: string, cb: (data: unknown, env: Envelope) => void) => {
    handlers.set(evt, cb)
    return () => handlers.delete(evt)
  }),
  send: vi.fn(), subscribeConversation: vi.fn(), sendMessage: vi.fn(),
}))
vi.mock('../../lib/ws', () => ({ getWsClient: () => ws }))
vi.mock('../../lib/api/client', async (original) => ({
  ...(await original<typeof import('../../lib/api/client')>()),
  getConversation: vi.fn(), conversationMessages: vi.fn(),
  blockConversation: vi.fn(), createReport: vi.fn(), onlineStats: vi.fn(), uploadMedia: vi.fn(),
}))

const PEER = { memberId: 2, nickname: '迷路的水獭', avatarSeed: 'seed-2', gender: 'female', age: 24, province: '浙江', tags: ['音乐'] }
const message = { id: 1, memberId: 2, nickname: PEER.nickname, avatarSeed: 'seed-2', kind: 'text', body: '历史消息', createdAt: '2026-11-14T10:00:00Z' }
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.pathname}{location.search}</output>
}
function renderMatch(path = '/match', strict = false) {
  const app = <MemoryRouter initialEntries={[path]}>
    <LocationProbe />
    <Routes><Route path="/match" element={<MatchPage />} /><Route path="/" element={<p>大厅</p>} /></Routes>
  </MemoryRouter>
  return render(strict ? <StrictMode>{app}</StrictMode> : app)
}
async function emit(type: string, data: unknown, id?: string) {
  await act(async () => { handlers.get(type)?.(data, { t: type, id, d: data }) })
}
async function found(peer: Record<string, unknown> = PEER, conversationId = 'c1') {
  await emit('match.found', { conversationId, peer })
}
async function enterChat() {
  await found()
  const overlay = screen.queryByRole('button', { name: '进入聊天' })
  if (overlay) fireEvent.click(overlay)
}
async function tick(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms) })
}
function requestId(type: string) {
  const index = ws.send.mock.calls.map(([t]) => t).lastIndexOf(type)
  expect(index).toBeGreaterThanOrEqual(0)
  const id = ws.send.mock.results[index].value as string
  expect(id).toEqual(expect.any(String))
  return id
}
function click(name: string) { fireEvent.click(screen.getByRole('button', { name })) }
function header() { return within(document.querySelector('header')!) }

beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  handlers.clear()
  let sequence = 0
  ws.send.mockImplementation(() => `request-${++sequence}`)
  useConversationsStore.getState().clearAll()
  useIdentityStore.setState({ nickname: '自己的昵称', avatarSeed: 'self-seed', me: {
    nickname: '自己的昵称', avatarSeed: 'self-seed', verified: false, verificationMode: 'off', publicCode: 'test',
    profile: { gender: 'secret', age: null, province: null, provincePublic: false, tags: ['音乐'], cleanMode: false },
    matchPrefs: { gender: 'any', sameProvince: false }, vip: { active: false, until: null }, broadcastCards: 0,
  } })
  vi.mocked(api.getConversation).mockImplementation(async (id) => ({ id, kind: 'match', myMemberId: 1, burnAfterRead: false,
    peer: { memberId: 2, nickname: '恢复的昵称', avatarSeed: 'resume-seed' } }))
  vi.mocked(api.conversationMessages).mockResolvedValue([])
  vi.mocked(api.onlineStats).mockResolvedValue({ online: 10, matching: 7, inRooms: 0 })
  vi.mocked(api.createReport).mockResolvedValue({ id: 'report-1' })
  vi.mocked(api.blockConversation).mockResolvedValue(undefined)
  document.title = '原标题'
  Object.defineProperty(navigator, 'vibrate', { configurable: true, value: vi.fn() })
})
afterEach(async () => {
  cleanup()
  await Promise.resolve()
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('MatchPage 1v1 头部', () => {
  it('显示对方真实昵称与头像种子，摘要各项为独立 span', async () => {
    renderMatch()
    await found()
    expect(header().getByText('迷路的水獭')).toBeInTheDocument()
    expect(document.querySelector('header [data-seed="seed-2"]')).not.toBeNull()
    for (const text of ['女', '24', '浙江']) expect(header().getByText(text, { exact: true }).tagName).toBe('SPAN')
    expect(screen.queryByText(/^匿名·/)).toBeNull()
  })
  it('对方没有昵称时回退「对方」', async () => {
    renderMatch()
    await found({ memberId: 2 })
    expect(header().getByText('对方', { exact: true })).toBeInTheDocument()
    expect(screen.queryByText(/^匿名·/)).toBeNull()
  })
})

describe('P3.2 搜索、自动开始和请求 id', () => {
  it('idle 只保留开始匹配与返回大厅，显示自己的头像', () => {
    renderMatch()
    expect(screen.getByRole('heading', { name: '随机匹配' })).toBeInTheDocument()
    expect(document.querySelector('[data-seed="self-seed"]')).not.toBeNull()
    click('开始匹配')
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
  })
  it('auto 首屏乐观等待，消费 auto 参数保留其它参数；取消发帧并回大厅', async () => {
    renderMatch('/match?auto=1&keep=1')
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '开始匹配' })).toBeNull()
    requestId('match.start')
    expect(screen.getByTestId('location')).toHaveTextContent('/match?keep=1')
    click('取消匹配')
    await act(async () => {})
    expect(ws.send.mock.calls.filter(([t]) => t === 'match.cancel')).toHaveLength(1)
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/)
  })
  it('等待人数立即拉取、每十秒刷新，失败后隐藏；提示轮换、30 秒硬条件建议', async () => {
    renderMatch('/match?auto=1')
    await act(async () => {})
    expect(screen.getByText(/7 人正在匹配/)).toBeInTheDocument()
    expect(screen.getByText('耐心一点，好聊的人值得等')).toBeInTheDocument()
    await tick(4000)
    expect(screen.getByText('匹配成功后会提醒你，可以先切出去')).toBeInTheDocument()
    vi.mocked(api.onlineStats).mockRejectedValueOnce(new Error('offline'))
    await tick(6000)
    expect(api.onlineStats).toHaveBeenCalledTimes(2)
    expect(screen.queryByText(/人正在匹配/)).toBeNull()
    await emit('match.waiting', { filters: { cleanMode: true, gender: 'female', sameProvince: false } })
    await tick(20_000)
    expect(screen.getByText('00:30')).toBeInTheDocument()
    expect(screen.getByText('条件比较严格，放宽一点会更快匹配到')).toBeInTheDocument()
    click('调整条件')
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/)
  })
  it.each([false, true])('同 id 错误回到 idle（已收到 waiting=%s）', async (waiting) => {
    renderMatch('/match?auto=1')
    const id = requestId('match.start')
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
    if (waiting) await emit('match.waiting', {})
    await emit('error', { code: 'internal' }, id)
    expect(screen.getByRole('button', { name: '开始匹配' })).toBeInTheDocument()
  })
  it('不同 id 错误不回滚；没有响应也不超时回滚；found 后迟到的同 id 错误不退聊天', async () => {
    renderMatch('/match?auto=1')
    const id = requestId('match.start')
    await emit('error', { code: 'rate_limited' }, 'other-id')
    await tick(60_000)
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
    await found()
    await emit('error', { code: 'internal' }, id)
    expect(header().getByText(PEER.nickname)).toBeInTheDocument()
  })
  it('旧请求错误不影响新一次开始匹配', async () => {
    renderMatch()
    click('开始匹配')
    const oldId = requestId('match.start')
    await emit('error', { code: 'internal' }, oldId)
    click('开始匹配')
    const newId = requestId('match.start')
    expect(newId).not.toBe(oldId)
    await emit('error', { code: 'internal' }, oldId)
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
    await emit('error', { code: 'internal' }, newId)
    expect(screen.getByRole('button', { name: '开始匹配' })).toBeInTheDocument()
  })
  it('StrictMode 只开始一次、模拟卸载不 cancel、真实卸载 cancel 一次', async () => {
    const view = renderMatch('/match?auto=1', true)
    await act(async () => {})
    expect(ws.send.mock.calls.filter(([t]) => t === 'match.start')).toHaveLength(1)
    expect(ws.send.mock.calls.filter(([t]) => t === 'match.cancel')).toHaveLength(0)
    view.unmount()
    await act(async () => {})
    expect(ws.send.mock.calls.filter(([t]) => t === 'match.cancel')).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })
  it('StrictMode 同 id 错误恢复 idle，不能被重复 setup 覆盖快照', async () => {
    renderMatch('/match?auto=1', true)
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
    await emit('error', { code: 'internal' }, requestId('match.start'))
    expect(screen.getByRole('button', { name: '开始匹配' })).toBeInTheDocument()
  })
  it('无 auto 参数时 resume 不发 match.start，resume 清理待开始请求', async () => {
    renderMatch()
    await emit('hello', { resumable: ['c1'] })
    expect(header().getByText('恢复的昵称')).toBeInTheDocument()
    expect(ws.send).not.toHaveBeenCalledWith('match.start', {})
  })
})

describe('P3.2 成功过渡与后台提醒', () => {
  it('提醒与过渡同时显示及切换会话时不产生 console.error', async () => {
    const errors = vi.spyOn(console, 'error')
    renderMatch()
    await found()
    expect(screen.getByRole('button', { name: '进入聊天' })).toBeInTheDocument()
    await found(PEER, 'c2')
    expect(errors).not.toHaveBeenCalled()
  })
  it('found 后立即 ended，同时撤销仍在显示的过渡与待闪烁标题', async () => {
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    renderMatch()
    await found()
    expect(screen.getByRole('button', { name: '进入聊天' })).toBeInTheDocument()
    await emit('match.ended', { reason: 'peer_left' })
    expect(screen.queryByRole('button', { name: '进入聊天' })).toBeNull()
    expect(document.querySelector('header')?.closest('[inert]')).toBeNull()
    await tick(2000)
    expect(document.title).toBe('原标题')
    expect(header().getByText('已离开')).toBeInTheDocument()
  })
  it('found 过渡 900ms 后消失，期间聊天区 inert；点击或 Esc 可提前关闭', async () => {
    renderMatch()
    await found()
    expect(screen.getByRole('button', { name: '进入聊天' })).toBeInTheDocument()
    expect(document.querySelector('header')?.closest('[inert]')).not.toBeNull()
    await tick(899)
    expect(screen.getByRole('button', { name: '进入聊天' })).toBeInTheDocument()
    await tick(1)
    expect(screen.queryByRole('button', { name: '进入聊天' })).toBeNull()
    expect(document.querySelector('header')?.closest('[inert]')).toBeNull()
    await found(PEER, 'c2')
    click('进入聊天')
    expect(screen.queryByRole('button', { name: '进入聊天' })).toBeNull()
    await found(PEER, 'c3')
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('button', { name: '进入聊天' })).toBeNull()
  })
  it.each(['match.ended', 'match.waiting'])('%s 撤销过渡和标题提醒，恢复原标题', async (type) => {
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    renderMatch()
    await found()
    await tick(1000)
    expect(document.title).toBe('【匹配成功】')
    expect(navigator.vibrate).toHaveBeenCalledWith(80)
    await emit(type, { reason: 'peer_left' })
    expect(screen.queryByRole('button', { name: '进入聊天' })).toBeNull()
    expect(document.title).toBe('原标题')
    await tick(2000)
    expect(document.title).toBe('原标题')
  })
  it('重复 found 只保留最新会话过渡，旧定时器不关闭新过渡', async () => {
    renderMatch()
    await found()
    await tick(500)
    await found({ ...PEER, nickname: '新对方' }, 'c2')
    await tick(400)
    expect(within(screen.getByRole('button', { name: '进入聊天' })).getByText('新对方')).toBeInTheDocument()
    await tick(500)
    expect(screen.queryByRole('button', { name: '进入聊天' })).toBeNull()
  })
  it('回到前台停止提醒；卸载恢复原标题并清理定时器', async () => {
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    const view = renderMatch()
    await found()
    await tick(1000)
    expect(document.title).toBe('【匹配成功】')
    hidden.mockReturnValue(false)
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    expect(document.title).toBe('原标题')
    await tick(2000)
    expect(document.title).toBe('原标题')
    hidden.mockReturnValue(true)
    await found(PEER, 'c2')
    await tick(1000)
    view.unmount()
    await act(async () => {})
    expect(document.title).toBe('原标题')
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('P3.3 聊天操作', () => {
  it('换一个需二次确认；3 秒撤销确认，5 秒无响应恢复；聊天仍可发送', async () => {
    renderMatch()
    await enterChat()
    click('换一个')
    expect(ws.send).not.toHaveBeenCalledWith('match.next', {})
    expect(screen.getByText('再点一次换下一个人，3 秒后自动取消')).toBeInTheDocument()
    await tick(3000)
    expect(screen.getByRole('button', { name: '换一个' })).toBeInTheDocument()
    click('换一个'); click('确认换人？')
    requestId('match.next')
    expect(screen.getByRole('button', { name: '换人中…' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('消息输入框'), { target: { value: '还可以聊' } })
    click('发送')
    expect(ws.sendMessage).toHaveBeenCalledWith('c1', '还可以聊')
    await tick(5000)
    expect(screen.getByRole('button', { name: '换一个' })).toBeEnabled()
  })
  it('新会话重置换人确认与旧定时器', async () => {
    renderMatch()
    await enterChat()
    click('换一个')
    await tick(2000)
    await found(PEER, 'c2'); click('进入聊天')
    expect(screen.getByRole('button', { name: '换一个' })).toBeInTheDocument()
    click('换一个')
    await tick(1000)
    expect(screen.getByRole('button', { name: '确认换人？' })).toBeInTheDocument()
    await tick(2000)
    expect(screen.getByRole('button', { name: '换一个' })).toBeInTheDocument()
  })
  it('换人请求中切换会话，旧请求定时器和迟到错误不影响新会话确认', async () => {
    renderMatch()
    await enterChat()
    click('换一个'); click('确认换人？')
    const oldId = requestId('match.next')
    await tick(4000)
    await found(PEER, 'c2'); click('进入聊天')
    click('换一个')
    await tick(1000)
    await emit('error', { code: 'internal' }, oldId)
    expect(screen.getByRole('button', { name: '确认换人？' })).toBeInTheDocument()
    await tick(2000)
    expect(screen.getByRole('button', { name: '换一个' })).toBeInTheDocument()
  })
  it.each([false, true])('match.next 同 id error（已 waiting=%s）按当前状态恢复', async (waiting) => {
    renderMatch()
    await enterChat()
    click('换一个'); click('确认换人？')
    const id = requestId('match.next')
    await emit('error', { code: 'internal' }, 'unrelated')
    expect(screen.getByRole('button', { name: '换人中…' })).toBeDisabled()
    if (waiting) await emit('match.waiting', {})
    await emit('error', { code: 'internal' }, id)
    expect(screen.getByRole('button', { name: waiting ? '开始匹配' : '换一个' })).toBeInTheDocument()
    if (!waiting) expect(screen.getByLabelText('消息输入框')).toBeEnabled()
  })
  it('实际换人事件顺序 ended → waiting → 同 id error 仍能回到 idle', async () => {
    renderMatch()
    await enterChat()
    click('换一个'); click('确认换人？')
    const id = requestId('match.next')
    await emit('match.ended', { conversationId: 'c1', reason: 'peer_skipped' })
    await emit('match.waiting', {})
    await emit('error', { code: 'internal' }, id)
    expect(screen.getByRole('button', { name: '开始匹配' })).toBeInTheDocument()
  })
  it('更多操作 → 举报；屏蔽打开二次确认', async () => {
    renderMatch()
    await enterChat()
    click('更多操作')
    expect(screen.getByRole('dialog', { name: '更多操作' })).toBeInTheDocument()
    click('举报')
    expect(screen.getByRole('dialog', { name: '举报' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: '更多操作' })).toBeNull()
    click('取消')
    click('更多操作'); click('屏蔽')
    expect(screen.getByRole('dialog', { name: '屏蔽确认' })).toBeInTheDocument()
  })
  it('返回先确认，继续聊不离开，结束聊天发 match.leave 并回首页', async () => {
    renderMatch()
    await enterChat()
    click('返回')
    expect(screen.getByRole('dialog', { name: '结束这段对话？' })).toBeInTheDocument()
    click('继续聊')
    expect(ws.send).not.toHaveBeenCalledWith('match.leave', {})
    click('返回'); click('结束聊天')
    expect(ws.send).toHaveBeenCalledWith('match.leave', {})
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/)
  })
  it('共同标签生成破冰语，点击直接发送，自己发过消息后隐藏', async () => {
    renderMatch()
    await enterChat()
    expect(screen.getByText('你们都喜欢 音乐，打个招呼吧')).toBeInTheDocument()
    click('你也喜欢音乐吗？')
    expect(ws.sendMessage).toHaveBeenCalledWith('c1', '你也喜欢音乐吗？')
    await emit('msg.new', { conversationId: 'c1', message: { ...message, memberId: 1, body: '我发的' } })
    expect(screen.queryByRole('button', { name: '嗨，你好呀' })).toBeNull()
  })
  it('resume 使用 chat.peer，结束后仍可举报正确 memberId；不补资料请求', async () => {
    vi.mocked(api.conversationMessages).mockResolvedValue([message])
    renderMatch()
    await emit('hello', { resumable: ['c1'] })
    expect(header().getByText('恢复的昵称')).toBeInTheDocument()
    expect(document.querySelector('header [data-seed="resume-seed"]')).not.toBeNull()
    expect(api.getConversation).toHaveBeenCalledTimes(1)
    await emit('match.ended', { reason: 'peer_left' })
    click('更多操作'); click('举报')
    const dialog = within(screen.getByRole('dialog', { name: '举报' }))
    fireEvent.click(dialog.getByRole('checkbox'))
    fireEvent.change(dialog.getByLabelText('举报原因'), { target: { value: 'harassment' } })
    click('提交举报')
    await act(async () => {})
    expect(api.createReport).toHaveBeenCalledWith(expect.objectContaining({ conversationId: 'c1', memberId: 2, messageIds: [1] }))
  })
})

describe('P3.4 原地结束', () => {
  it('ended 的头部返回直接回大厅，不弹确认、不发送 match.leave', async () => {
    renderMatch()
    await enterChat()
    await emit('match.ended', { reason: 'peer_left' })
    click('返回')
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/)
    expect(screen.queryByRole('dialog', { name: '结束这段对话？' })).toBeNull()
    expect(ws.send).not.toHaveBeenCalledWith('match.leave', {})
  })
  it.each(['ended', 'new-conversation'])('上传未完成时 %s，不向结束或旧会话发送媒体', async (change) => {
    let complete!: (media: api.UploadedMedia) => void
    vi.mocked(api.uploadMedia).mockReturnValueOnce(new Promise((resolve) => { complete = resolve }))
    renderMatch()
    await enterChat()
    fireEvent.change(screen.getByLabelText('选择图片或视频'), { target: { files: [new File(['x'], 'photo.png', { type: 'image/png' })] } })
    if (change === 'ended') await emit('match.ended', { reason: 'peer_left' })
    else await found(PEER, 'c2')
    await act(async () => { complete({ mediaId: 'm1', kind: 'image' }) })
    expect(ws.sendMessage).not.toHaveBeenCalled()
  })
  it('ended 保留历史与结束原因，禁用发送和破冰语；再来一个乐观等待，失败恢复 ended 快照', async () => {
    vi.mocked(api.conversationMessages).mockResolvedValue([message])
    renderMatch()
    await enterChat()
    expect(screen.getByRole('button', { name: '嗨，你好呀' })).toBeInTheDocument()
    const input = screen.getByLabelText('消息输入框')
    fireEvent.change(input, { target: { value: '不能发出' } })
    const staleSend = screen.getByRole('button', { name: '发送' })
    await emit('match.ended', { reason: 'peer_skipped' })
    fireEvent.click(staleSend)
    expect(ws.sendMessage).not.toHaveBeenCalled()
    expect(screen.getByText('历史消息')).toBeInTheDocument()
    expect(screen.getByText('对方跳过了你')).toBeInTheDocument()
    expect(header().getByText('已离开')).toBeInTheDocument()
    expect(screen.queryByLabelText('消息输入框')).toBeNull()
    expect(screen.queryByRole('button', { name: '嗨，你好呀' })).toBeNull()
    expect(screen.queryByRole('button', { name: '换一个' })).toBeNull()
    click('再来一个')
    const id = requestId('match.start')
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
    await emit('match.waiting', {})
    await emit('error', { code: 'internal' }, id)
    expect(screen.getByText('历史消息')).toBeInTheDocument()
    expect(screen.getByText('对方跳过了你')).toBeInTheDocument()
    expect(screen.getByText('对话已结束，聊天记录仍可往上翻看')).toBeInTheDocument()
    click('再来一个')
    await emit('match.waiting', {})
    expect(screen.getByText('正在寻找有趣的人')).toBeInTheDocument()
  })
})
